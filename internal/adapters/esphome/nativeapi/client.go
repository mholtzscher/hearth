// Package nativeapi is a private ESPHome Native API client: selected transport, framing, and the message subset that
// hearth-adapter-esphome consumes. It owns no reconnect policy; callers dial a new Conn for each connection
// generation.
package nativeapi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
)

const (
	// clientAPIMajor and clientAPIMinor are the API version of the vendored schema (ESPHome 2026.10.2).
	clientAPIMajor = 1
	clientAPIMinor = 19
	// minimumServerAPIMinor is API 1.10, observed on the AIR-1 running ESPHome 2024.11.1.
	minimumServerAPIMinor = 10
	// authenticationRemovedMinor is API 1.14 (ESPHome 2026.1), without legacy authentication.
	authenticationRemovedMinor = 14

	// idleBeforePing is how long the client waits without incoming traffic before it pings.
	idleBeforePing = 20 * time.Second
	// silenceLimit ends a connection that has sent nothing for this long.
	silenceLimit = 60 * time.Second
	// writeTimeout bounds every frame write.
	writeTimeout = 2 * time.Second
	// disconnectWait bounds how long Close waits for the device to acknowledge a disconnect.
	disconnectWait = time.Second
	// eventBuffer bounds undelivered state pushes.
	eventBuffer = 256
	// maximumEntities bounds one ListEntities answer.
	maximumEntities = 512
	// readBufferSize is the socket read buffer.
	readBufferSize = 4096
)

// DialConfig selects one device and the identity it must prove.
type DialConfig struct {
	Address string
	Port    uint16
	PSK     PSK
	// InsecurePlaintext explicitly selects unencrypted TCP. The secure default never falls back to this mode.
	InsecurePlaintext bool
	// ExpectMAC is checked against the server hello's MAC when the firmware sends one. Any common MAC text form is
	// accepted; an empty value disables the check.
	ExpectMAC  string
	ClientInfo string
}

// Dialer opens ready connections. It is the seam between the Adapter and the network.
type Dialer interface {
	// Dial connects using the selected transport, completes hello and authentication, and returns a ready Conn.
	// ctx bounds the whole sequence.
	Dial(ctx context.Context, config DialConfig) (Conn, error)
}

// Conn is one live API connection using its explicitly selected transport.
type Conn interface {
	Server() ServerInfo
	DeviceInfo(ctx context.Context) (DeviceInfo, error)
	ListEntities(ctx context.Context) ([]EntityInfo, error)
	// SubscribeStates starts state delivery on Events. Call it once per Conn.
	SubscribeStates(ctx context.Context) error
	// Events delivers state pushes. It closes when the connection ends; Err then reports why.
	Events() <-chan StateUpdate
	// Done closes immediately when the generation ends, before buffered Events drain.
	Done() <-chan struct{}
	// Err reports why the connection ended, or nil while it is open.
	Err() error
	Send(ctx context.Context, command Command) error
	// Close disconnects politely and joins the connection's goroutines. It is idempotent.
	Close() error
}

// NetDialer dials TCP.
type NetDialer struct{}

// Dial implements Dialer over TCP.
func (NetDialer) Dial(ctx context.Context, config DialConfig) (Conn, error) {
	var dialer net.Dialer
	network, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(config.Address, strconv.Itoa(int(config.Port))))
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	conn, err := Open(ctx, network, config)
	if err != nil {
		_ = network.Close()
		return nil, err
	}
	return conn, nil
}

// Open runs the handshake and hello on an established stream and starts the connection's goroutines. ctx bounds the
// handshake. On error the caller still owns network.
func Open(ctx context.Context, network net.Conn, config DialConfig) (Conn, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := network.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("set handshake deadline: %w", err)
		}
	}
	stop := context.AfterFunc(ctx, func() { _ = network.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	reader := bufio.NewReaderSize(network, readBufferSize)
	transport, err := openTransport(reader, network, config)
	if err != nil {
		return nil, contextError(ctx, err)
	}
	server, err := exchangeHello(transport, config.ClientInfo)
	if err != nil {
		return nil, contextError(ctx, err)
	}
	if !stop() {
		return nil, ctx.Err()
	}
	if setDeadlineErr := network.SetDeadline(time.Time{}); setDeadlineErr != nil {
		return nil, fmt.Errorf("clear handshake deadline: %w", setDeadlineErr)
	}

	conn := &conn{
		network:       network,
		transport:     transport,
		server:        server,
		events:        make(chan StateUpdate, eventBuffer),
		done:          make(chan struct{}),
		disconnectAck: make(chan struct{}),
		writeGate:     make(chan struct{}, 1),
		requestGate:   make(chan struct{}, 1),
		activity:      make(chan struct{}, 1),
	}
	conn.lastRead.Store(time.Now().UnixNano())
	conn.workers.Add(2) //nolint:mnd // The reader and the keepalive loop.
	go conn.readLoop()
	go conn.keepalive() //nolint:gosec // The connection owns this loop beyond the handshake context; Close joins it.
	if server.APIMinor < authenticationRemovedMinor {
		if authErr := conn.authenticateWithoutPassword(ctx); authErr != nil {
			conn.fail(authErr)
			conn.workers.Wait()
			return nil, contextError(ctx, conn.Err())
		}
	}
	return conn, nil
}

// exchangeHello sends HelloRequest and validates HelloResponse before any goroutine starts.
func exchangeHello(transport messageTransport, clientInfo string) (ServerInfo, error) {
	id, data, err := encode(&apipb.HelloRequest{
		ClientInfo:      clientInfo,
		ApiVersionMajor: clientAPIMajor,
		ApiVersionMinor: clientAPIMinor,
	})
	if err != nil {
		return ServerInfo{}, err
	}
	if writeMessageErr := transport.writeMessage(id, data); writeMessageErr != nil {
		return ServerInfo{}, fmt.Errorf("send hello: %w", writeMessageErr)
	}
	for {
		messageID, payload, readMessageErr := transport.readMessage()
		if readMessageErr != nil {
			return ServerInfo{}, fmt.Errorf("read hello: %w", readMessageErr)
		}
		if messageID != idHelloResponse {
			continue
		}
		var response apipb.HelloResponse
		if decodeIntoErr := decodeInto(payload, &response); decodeIntoErr != nil {
			return ServerInfo{}, decodeIntoErr
		}
		server := ServerInfo{
			Name:     response.GetName(),
			APIMajor: response.GetApiVersionMajor(),
			APIMinor: response.GetApiVersionMinor(),
		}
		if server.APIMajor != clientAPIMajor || server.APIMinor < minimumServerAPIMinor {
			return server, fmt.Errorf("%w: device reports %d.%d", ErrIncompatibleAPI, server.APIMajor, server.APIMinor)
		}
		return server, nil
	}
}

// conn owns one connection. The reader goroutine is the only receiver of frames and the only sender on events.
type conn struct {
	network   net.Conn
	transport messageTransport
	server    ServerInfo

	writeGate chan struct{}

	// requestGate admits one request at a time; mu guards the current waiter.
	requestGate chan struct{}
	mu          sync.Mutex
	waiter      *waiter

	subscribed atomic.Bool
	closing    atomic.Bool
	events     chan StateUpdate

	lastRead atomic.Int64
	activity chan struct{}

	failOnce      sync.Once
	closeOnce     sync.Once
	ackOnce       sync.Once
	err           error
	done          chan struct{}
	disconnectAck chan struct{}
	workers       sync.WaitGroup
}

// waiter receives the responses one request expects.
type waiter struct {
	accepts  func(uint16) bool
	messages chan frame
	cancel   chan struct{}
}

type frame struct {
	id   uint16
	data []byte
}

func (c *conn) Server() ServerInfo         { return c.server }
func (c *conn) Events() <-chan StateUpdate { return c.events }
func (c *conn) Done() <-chan struct{}      { return c.done }
func (c *conn) touch() {
	c.lastRead.Store(time.Now().UnixNano())
	select {
	case c.activity <- struct{}{}:
	default:
	}
}
func (c *conn) idle() time.Duration { return time.Since(time.Unix(0, c.lastRead.Load())) }

// Err reports why the connection ended, or nil while it is open.
func (c *conn) Err() error {
	if c.closed() {
		return c.err
	}
	return nil
}

func (c *conn) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *conn) DeviceInfo(ctx context.Context) (DeviceInfo, error) {
	var info DeviceInfo
	err := c.request(ctx, &apipb.DeviceInfoRequest{}, func(id uint16) bool { return id == idDeviceInfoResponse },
		func(message frame) (bool, error) {
			decoded, err := decodeDeviceInfo(message.data)
			info = decoded
			return true, err
		})
	return info, err
}

func (c *conn) ListEntities(ctx context.Context) ([]EntityInfo, error) {
	var entities []EntityInfo
	accepts := func(id uint16) bool {
		_, listed := apiSchema.listEntities[id]
		return listed || id == idListEntitiesDoneResponse
	}
	err := c.request(ctx, &apipb.ListEntitiesRequest{}, accepts, func(message frame) (bool, error) {
		if message.id == idListEntitiesDoneResponse {
			return true, nil
		}
		if len(entities) == maximumEntities {
			return true, fmt.Errorf("esphome: device lists more than %d entities", maximumEntities)
		}
		entity, err := decodeEntity(message.id, message.data)
		if err != nil {
			return true, err
		}
		entities = append(entities, entity)
		return false, nil
	})
	return entities, err
}

func (c *conn) SubscribeStates(ctx context.Context) error {
	if !c.subscribed.CompareAndSwap(false, true) {
		return errors.New("esphome: states already subscribed")
	}
	return c.write(ctx, &apipb.SubscribeStatesRequest{})
}

func (c *conn) Send(ctx context.Context, command Command) error {
	message, err := commandMessage(command)
	if err != nil {
		return err
	}
	return c.write(ctx, message)
}

func (c *conn) Close() error {
	c.closeOnce.Do(func() {
		c.closing.Store(true)
		if !c.closed() && c.writeInternal(context.Background(), &apipb.DisconnectRequest{}, true) == nil {
			timer := time.NewTimer(disconnectWait)
			select {
			case <-c.disconnectAck:
			case <-c.done:
			case <-timer.C:
			}
			timer.Stop()
		}
		c.fail(ErrClosed)
		c.workers.Wait()
	})
	return nil
}

// request sends one request and feeds each accepted response to handle until handle reports completion.
func (c *conn) request(
	ctx context.Context,
	message proto.Message,
	accepts func(uint16) bool,
	handle func(frame) (bool, error),
) error {
	select {
	case c.requestGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.err
	}
	defer func() { <-c.requestGate }()
	current := &waiter{accepts: accepts, messages: make(chan frame, 1), cancel: make(chan struct{})}
	c.mu.Lock()
	c.waiter = current
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.waiter = nil
		c.mu.Unlock()
		close(current.cancel)
	}()
	if err := c.write(ctx, message); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			// Responses carry no request ID. Reuse after cancellation could misattribute this request's tail.
			c.fail(ctx.Err())
			return ctx.Err()
		case <-c.done:
			return c.err
		case response := <-current.messages:
			complete, err := handle(response)
			if err != nil || complete {
				return err
			}
		}
	}
}

// write encodes and sends one message under the write lock and the write timeout. A failed write ends the
// connection, because a partial frame corrupts the stream.
func (c *conn) write(ctx context.Context, message proto.Message) error {
	return c.writeInternal(ctx, message, false)
}

func (c *conn) writeInternal(ctx context.Context, message proto.Message, duringClose bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closing.Load() && !duringClose {
		return ErrClosed
	}
	if c.closed() {
		return c.err
	}
	id, data, err := encode(message)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(writeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	select {
	case c.writeGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.err
	}
	defer func() { <-c.writeGate }()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if c.closing.Load() && !duringClose {
		return ErrClosed
	}
	if c.closed() {
		return c.err
	}
	stopCancel := context.AfterFunc(ctx, func() { c.fail(ctx.Err()) })
	defer stopCancel()
	if setWriteDeadlineErr := c.network.SetWriteDeadline(deadline); setWriteDeadlineErr != nil {
		c.fail(setWriteDeadlineErr)
		return setWriteDeadlineErr
	}
	if writeMessageErr := c.transport.writeMessage(id, data); writeMessageErr != nil {
		if errors.Is(writeMessageErr, ErrFrameTooLarge) {
			return writeMessageErr
		}
		c.fail(fmt.Errorf("write: %w", writeMessageErr))
		return writeMessageErr
	}
	return nil
}

// fail records the first cause, ends the connection, and wakes every waiter.
func (c *conn) fail(cause error) {
	c.failOnce.Do(func() {
		if c.closing.Load() {
			cause = ErrClosed
		}
		c.err = cause
		close(c.done)
		_ = c.network.Close()
	})
}

func (c *conn) readLoop() {
	defer c.workers.Done()
	defer close(c.events)
	for {
		id, data, err := c.transport.readMessage()
		if err != nil {
			c.fail(fmt.Errorf("read: %w", err))
			return
		}
		c.touch()
		if !c.dispatch(id, data) {
			return
		}
	}
}

// dispatch handles one received message and reports whether reading continues.
func (c *conn) dispatch(id uint16, data []byte) bool {
	switch id {
	case idAuthenticationResponse:
		if err := checkAuthenticationResponse(data); err != nil {
			c.fail(err)
			return false
		}
	case idPingRequest:
		_ = c.write(context.Background(), &apipb.PingResponse{})
	case idPingResponse:
	case idDisconnectRequest:
		_ = c.write(context.Background(), &apipb.DisconnectResponse{})
		c.fail(errors.New("esphome: device requested disconnect"))
		return false
	case idDisconnectResponse:
		c.ackOnce.Do(func() { close(c.disconnectAck) })
	default:
		update, isState, err := decodeState(id, data)
		if err != nil {
			c.fail(err)
			return false
		}
		if isState {
			update.ReceivedAt = time.Now().UTC()
			return c.deliver(update)
		}
		c.respond(frame{id: id, data: data})
	}
	return !c.closed()
}

// deliver queues a state push without blocking; a full buffer ends the connection visibly.
func (c *conn) deliver(update StateUpdate) bool {
	if !c.subscribed.Load() {
		return true
	}
	select {
	case c.events <- update:
		return true
	default:
		c.fail(ErrSlowConsumer)
		return false
	}
}

// respond hands a response to the current waiter, if it expects one. Other messages are ignored.
func (c *conn) respond(message frame) {
	c.mu.Lock()
	current := c.waiter
	c.mu.Unlock()
	if current == nil || !current.accepts(message.id) {
		return
	}
	select {
	case current.messages <- message:
	case <-current.cancel:
	case <-c.done:
	}
}

// keepalive pings after idleBeforePing without traffic and ends the connection after silenceLimit.
func (c *conn) keepalive() {
	defer c.workers.Done()
	timer := time.NewTimer(idleBeforePing)
	defer timer.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-timer.C:
		case <-c.activity:
		}
		idle := c.idle()
		switch {
		case idle >= silenceLimit:
			c.fail(ErrKeepaliveTimeout)
			return
		case idle >= idleBeforePing:
			_ = c.write(context.Background(), &apipb.PingRequest{})
			timer.Reset(min(idleBeforePing, silenceLimit-idle))
		default:
			timer.Reset(idleBeforePing - idle)
		}
	}
}

// contextError prefers the context's error when the handshake failed because the context ended.
func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	// The socket and context share a deadline. The socket may wake first, before the context's timer callback.
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		if timeout, timedOut := errors.AsType[net.Error](err); timedOut && timeout.Timeout() {
			return context.DeadlineExceeded
		}
	}
	return err
}

// compactMAC lowercases a MAC and removes separators, so "AA:BB:..." and "aabb..." compare equal.
func compactMAC(text string) string {
	return strings.ToLower(strings.NewReplacer(":", "", "-", "").Replace(text))
}
