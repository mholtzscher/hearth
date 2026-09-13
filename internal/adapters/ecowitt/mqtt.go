package ecowitt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// mqttRelayCapacity bounds the Paho callback relay in messages. A callback
// that would exceed it signals overflow instead of dropping or evicting a
// report, which ends the MQTT generation visibly and reconnects.
const mqttRelayCapacity = 64

// mqttProtocolVersion311 is the MQTT 3.1.1 protocol level. The captured
// GW2000B broker traffic is MQTT 3.1.1.
const mqttProtocolVersion311 = 4

// errMQTTRelayOverflow ends the current MQTT generation because the Paho
// callback relay is full. It is a plain sentinel: it never carries a topic, a
// payload, or a value.
var errMQTTRelayOverflow = errors.New("ecowitt MQTT relay overflow; reconnecting to resynchronize")

// errMQTTSubscribeRejected reports that the broker refused the exact-topic
// subscription with the MQTT 3.1.1 SUBACK failure return code 0x80. It is a
// plain sentinel: the rejected topic, whose second segment is commonly a
// station MAC, never reaches the diagnostic.
var errMQTTSubscribeRejected = errors.New("ecowitt MQTT broker rejected the subscription")

// mqttSubscribeFailureReturnCode is the MQTT 3.1.1 SUBACK failure return code.
const mqttSubscribeFailureReturnCode = 0x80

// mqttDialer is the package-private MQTT transport seam. No MQTT interface
// leaves this package and v1 publishes nothing to MQTT.
type mqttDialer interface {
	Dial(context.Context, mqttConfig, func(mqttMessage)) (mqttConnection, error)
}

// mqttConnection is one established subscription.
type mqttConnection interface {
	Subscribe(context.Context, string, byte) error
	Lost() <-chan error
	Close()
}

// mqttConfig carries the exact-topic Paho settings derived from validated
// configuration.
type mqttConfig struct {
	URL      string
	ClientID string
}

// mqttMessage is one copied MQTT delivery. Payload ownership never remains
// with Paho: the callback copies the topic and payload bytes and assigns one
// local receipt time before enqueueing the message. The receipt time keeps its
// Go monotonic reading so deadline arithmetic never follows a wall-clock step;
// SDK wire formatting converts it to UTC.
type mqttMessage struct {
	Topic      string
	Payload    []byte
	Retained   bool
	ReceivedAt time.Time
}

// pahoDialer is the production MQTT 3.1.1 transport.
type pahoDialer struct{}

// newPahoDialer constructs the production dialer.
func newPahoDialer() mqttDialer { return pahoDialer{} }

// Dial connects one Paho MQTT 3.1.1 client with a clean session and no
// automatic reconnect, then relays copied messages through a bounded queue. A
// bounded token wait bounds the initial connect. The relay is connection-owned
// so its admitted deliveries outlive the connection context, and Close waits
// for them to drain.
func (pahoDialer) Dial(
	ctx context.Context,
	config mqttConfig,
	receive func(mqttMessage),
) (mqttConnection, error) {
	relay := newMQTTRelay(receive)
	lost := make(chan error, 1)
	messageHandler := func(_ paho.Client, message paho.Message) {
		relay.enqueue(mqttMessage{
			Topic:      message.Topic(),
			Payload:    append([]byte(nil), message.Payload()...),
			Retained:   message.Retained(),
			ReceivedAt: receivedAtNow(),
		})
	}
	go relay.watchOverflow(ctx, lost)
	options := newPahoClientOptions(config, func(err error) {
		relay.seal()
		signalMQTTLoss(lost, err)
	})
	options.SetDefaultPublishHandler(messageHandler)
	client := paho.NewClient(options)
	connection := &pahoConnection{client: client, messageHandler: messageHandler, relay: relay, lost: lost}
	if err := waitPahoToken(ctx, client.Connect()); err != nil {
		connection.Close()
		return nil, fmt.Errorf("connect Ecowitt MQTT broker: %w", err)
	}
	return connection, nil
}

// receivedAtNow returns the local receipt time for one MQTT delivery. It keeps
// the Go monotonic reading so report and measurement deadlines never follow a
// wall-clock step. Only the SDK wire timestamp fields are formatted as UTC.
func receivedAtNow() time.Time { return time.Now() }

// newPahoClientOptions pins the exact MQTT 3.1.1 clean-session contract. The
// Adapter owns reconnect with bounded backoff, so Paho's automatic reconnect
// and initial connect retry stay disabled.
func newPahoClientOptions(config mqttConfig, onLost func(error)) *paho.ClientOptions {
	options := paho.NewClientOptions().
		AddBroker(normalizePahoBrokerURL(config.URL)).
		SetClientID(config.ClientID).
		SetProtocolVersion(mqttProtocolVersion311).
		SetCleanSession(true).
		SetAutoReconnect(false).
		SetConnectRetry(false).
		SetOrderMatters(true)
	options.SetConnectionLostHandler(func(_ paho.Client, err error) {
		onLost(err)
	})
	return options
}

// normalizePahoBrokerURL maps the operator's mqtt:// scheme to Paho's tcp://
// scheme. Any other scheme is already normalized or rejected by configuration
// validation.
func normalizePahoBrokerURL(brokerURL string) string {
	if remainder, ok := strings.CutPrefix(brokerURL, "mqtt://"); ok {
		return "tcp://" + remainder
	}
	return brokerURL
}

// signalMQTTLoss records one connection-loss cause without blocking a Paho
// callback. The first cause wins: a relay overflow and a later broker
// disconnect describe the same ended generation.
func signalMQTTLoss(lost chan<- error, err error) {
	if err == nil {
		err = errors.New("ecowitt MQTT connection lost")
	}
	select {
	case lost <- err:
	default:
	}
}

// pahoConnection is one live Paho client with its bounded relay.
type pahoConnection struct {
	client         paho.Client
	messageHandler paho.MessageHandler
	relay          *mqttRelay
	lost           chan error
	closeOnce      sync.Once
}

// Subscribe requests the exact configured topic at the requested QoS. A broker
// SUBACK 0x80 is inspected from the SubscribeToken result, because Paho reports
// it there rather than in Token.Error. The error deliberately omits the topic,
// whose second segment is commonly a station MAC.
func (connection *pahoConnection) Subscribe(ctx context.Context, topic string, qos byte) error {
	token := connection.client.Subscribe(topic, qos, connection.messageHandler)
	if err := waitPahoToken(ctx, token); err != nil {
		return fmt.Errorf("subscribe Ecowitt MQTT topic: %w", err)
	}
	if err := subscriptionRejected(token); err != nil {
		return err
	}
	return nil
}

// subscriptionRejected inspects one completed SubscribeToken result and reports
// the fixed rejection sentinel when any filter was refused with SUBACK 0x80. A
// token without a result, or a result with granted QoS codes, is accepted. The
// topic keys of the result map are never read into a diagnostic.
func subscriptionRejected(token paho.Token) error {
	result, ok := token.(interface{ Result() map[string]byte })
	if !ok {
		return nil
	}
	for _, returnCode := range result.Result() {
		if returnCode == mqttSubscribeFailureReturnCode {
			return errMQTTSubscribeRejected
		}
	}
	return nil
}

// Lost reports the first connection-loss cause.
func (connection *pahoConnection) Lost() <-chan error { return connection.lost }

// Close seals the relay, waits for every admitted delivery to drain, then
// disconnects the client. Sealing first stops new callbacks; waiting means the
// coordinator has received every admitted copied report before the supervisor
// reports the generation unavailable, and the wait is bounded because the drain
// submits on the Adapter's run context, which shutdown cancellation releases.
func (connection *pahoConnection) Close() {
	connection.closeOnce.Do(func() {
		connection.relay.seal()
		connection.relay.wait()
		connection.client.Disconnect(0)
	})
}

// waitPahoToken bounds every Paho token wait by the caller's context, so a
// stalled connect, subscribe, or publish can never outlive its generation.
func waitPahoToken(ctx context.Context, token paho.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-token.Done():
		return token.Error()
	}
}

// mqttRelay copies Paho callbacks into a bounded queue drained by one relay
// goroutine. Admission is nonblocking and bounded: a delivery that would exceed
// mqttRelayCapacity seals the relay and signals overflow instead of blocking a
// Paho callback or evicting an admitted delivery, which ends the MQTT
// generation visibly and reconnects. Sealing stops new admissions but never
// discards an admitted one, because an admitted delivery is already copied
// evidence: the drain delivers every admitted message in arrival order before
// it finishes, and a callback that arrives after the seal is ignored.
type mqttRelay struct {
	mutex  sync.Mutex
	queued []mqttMessage
	sealed bool

	// wake carries one nonblocking wake-up to the drain goroutine. It is
	// buffered so a producer never waits for the consumer.
	wake chan struct{}
	// overflowSignal is closed once when a delivery exceeds the bound.
	overflowSignal chan struct{}
	// done is closed when the drain has delivered every admitted message and
	// exited.
	done chan struct{}

	overflowOnce sync.Once
	deliver      func(mqttMessage)
}

// newMQTTRelay starts the drain goroutine for one MQTT connection.
func newMQTTRelay(deliver func(mqttMessage)) *mqttRelay {
	relay := &mqttRelay{
		queued:         make([]mqttMessage, 0, mqttRelayCapacity),
		wake:           make(chan struct{}, 1),
		overflowSignal: make(chan struct{}),
		done:           make(chan struct{}),
		deliver:        deliver,
	}
	go relay.run()
	return relay
}

// enqueue admits one copied message, or seals the relay and signals overflow
// when the bounded queue is full. It never blocks a Paho callback, never evicts
// an admitted message, and ignores a callback that arrives after the relay
// sealed.
func (relay *mqttRelay) enqueue(message mqttMessage) {
	relay.mutex.Lock()
	if relay.sealed {
		relay.mutex.Unlock()
		return
	}
	if len(relay.queued) >= mqttRelayCapacity {
		// Seal before signalling, so no further callback is admitted while the
		// generation ends and every message admitted so far still drains.
		relay.sealed = true
		relay.mutex.Unlock()
		relay.overflowOnce.Do(func() { close(relay.overflowSignal) })
		relay.wakeDrain()
		return
	}
	relay.queued = append(relay.queued, message)
	relay.mutex.Unlock()
	relay.wakeDrain()
}

// seal stops admitting new deliveries without discarding an admitted one. The
// drain continues until the queue is empty.
func (relay *mqttRelay) seal() {
	relay.mutex.Lock()
	relay.sealed = true
	relay.mutex.Unlock()
	relay.wakeDrain()
}

// wait blocks until the drain has delivered every admitted message and exited.
func (relay *mqttRelay) wait() { <-relay.done }

// overflowed reports that the bounded queue overflowed, which ends the
// generation visibly.
func (relay *mqttRelay) overflowed() <-chan struct{} { return relay.overflowSignal }

// wakeDrain wakes the drain goroutine without ever blocking the caller.
func (relay *mqttRelay) wakeDrain() {
	select {
	case relay.wake <- struct{}{}:
	default:
	}
}

// run delivers admitted messages in arrival order until the relay is sealed and
// its queue is empty. The empty-and-sealed decision is made under one mutex
// acquisition, so a delivery admitted just before the seal is still drained
// instead of being lost to a seal race.
func (relay *mqttRelay) run() {
	defer close(relay.done)
	for {
		relay.mutex.Lock()
		if len(relay.queued) > 0 {
			message := relay.queued[0]
			relay.queued[0] = mqttMessage{}
			relay.queued = relay.queued[1:]
			relay.mutex.Unlock()
			relay.deliver(message)
			continue
		}
		sealed := relay.sealed
		relay.mutex.Unlock()
		if sealed {
			return
		}
		<-relay.wake
	}
}

// watchOverflow surfaces a full relay as a connection loss so the connection
// supervisor ends the generation and reconnects. Sealing happens before the
// loss is signalled, and the signal never waits for the admitted deliveries to
// drain.
func (relay *mqttRelay) watchOverflow(ctx context.Context, lost chan<- error) {
	select {
	case <-ctx.Done():
	case <-relay.overflowSignal:
		relay.seal()
		signalMQTTLoss(lost, errMQTTRelayOverflow)
	}
}
