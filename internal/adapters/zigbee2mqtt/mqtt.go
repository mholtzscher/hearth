package zigbee2mqtt

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type mqttDialer interface {
	Dial(context.Context, mqttConfig, func(mqttMessage)) (mqttConnection, error)
}

type mqttConnection interface {
	Subscribe(context.Context, string, byte) error
	Publish(context.Context, string, byte, bool, []byte) error
	Lost() <-chan error
	Close()
}

type mqttConfig struct {
	URL      string
	ClientID string
}

type mqttMessage struct {
	Topic      string
	Payload    []byte
	Retained   bool
	ReceivedAt time.Time
}

const mqttProtocolVersion311 = 4

type pahoDialer struct{}

func newPahoDialer() mqttDialer {
	return pahoDialer{}
}

func (pahoDialer) Dial(ctx context.Context, config mqttConfig, receive func(mqttMessage)) (mqttConnection, error) {
	relay := newMQTTRelay(receive)
	lost := make(chan error, 1)
	messageHandler := func(_ paho.Client, message paho.Message) {
		relay.enqueue(mqttMessage{
			Topic:      message.Topic(),
			Payload:    append([]byte(nil), message.Payload()...),
			Retained:   message.Retained(),
			ReceivedAt: time.Now().UTC(),
		})
	}
	options := newPahoClientOptions(config, func(err error) {
		relay.close()
		select {
		case lost <- err:
		default:
		}
	})
	options.SetDefaultPublishHandler(messageHandler)
	client := paho.NewClient(options)
	connection := &pahoConnection{
		client:         client,
		messageHandler: messageHandler,
		relay:          relay,
		lost:           lost,
	}
	if err := waitPahoToken(ctx, client.Connect()); err != nil {
		connection.Close()
		return nil, fmt.Errorf("connect MQTT: %w", err)
	}
	return connection, nil
}

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

func normalizePahoBrokerURL(brokerURL string) string {
	if remainder, ok := strings.CutPrefix(brokerURL, "mqtt://"); ok {
		return "tcp://" + remainder
	}
	return brokerURL
}

type pahoConnection struct {
	client         paho.Client
	messageHandler paho.MessageHandler
	relay          *mqttRelay
	lost           <-chan error
	closeOnce      sync.Once
}

func (connection *pahoConnection) Subscribe(ctx context.Context, topic string, qos byte) error {
	if err := waitPahoToken(ctx, connection.client.Subscribe(topic, qos, connection.messageHandler)); err != nil {
		return fmt.Errorf("subscribe MQTT topic %q: %w", topic, err)
	}
	return nil
}

func (connection *pahoConnection) Publish(
	ctx context.Context,
	topic string,
	qos byte,
	retained bool,
	payload []byte,
) error {
	payloadCopy := append([]byte(nil), payload...)
	if err := waitPahoToken(ctx, connection.client.Publish(topic, qos, retained, payloadCopy)); err != nil {
		return fmt.Errorf("publish MQTT topic %q: %w", topic, err)
	}
	return nil
}

func (connection *pahoConnection) Lost() <-chan error {
	return connection.lost
}

func (connection *pahoConnection) Close() {
	connection.closeOnce.Do(func() {
		connection.relay.close()
		connection.client.Disconnect(0)
	})
}

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

type mqttRelay struct {
	mutex   sync.Mutex
	ready   *sync.Cond
	queue   []mqttMessage
	closed  bool
	receive func(mqttMessage)
}

func newMQTTRelay(receive func(mqttMessage)) *mqttRelay {
	relay := &mqttRelay{receive: receive}
	relay.ready = sync.NewCond(&relay.mutex)
	go relay.run()
	return relay
}

func (relay *mqttRelay) enqueue(message mqttMessage) {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	if relay.closed {
		return
	}
	relay.queue = append(relay.queue, message)
	relay.ready.Signal()
}

func (relay *mqttRelay) close() {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	relay.closed = true
	relay.queue = nil
	relay.ready.Broadcast()
}

func (relay *mqttRelay) run() {
	for {
		relay.mutex.Lock()
		for !relay.closed && len(relay.queue) == 0 {
			relay.ready.Wait()
		}
		if relay.closed {
			relay.mutex.Unlock()
			return
		}
		message := relay.queue[0]
		relay.queue[0] = mqttMessage{}
		relay.queue = relay.queue[1:]
		if len(relay.queue) == 0 {
			relay.queue = nil
		}
		relay.mutex.Unlock()

		relay.receive(message)
	}
}
