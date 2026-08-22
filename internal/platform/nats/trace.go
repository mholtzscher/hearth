package nats

import natsgo "github.com/nats-io/nats.go"

type HeaderCarrier natsgo.Header

func (carrier HeaderCarrier) Get(key string) string {
	return natsgo.Header(carrier).Get(key)
}

func (carrier HeaderCarrier) Set(key, value string) {
	natsgo.Header(carrier).Set(key, value)
}

func (carrier HeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(carrier))
	for key := range carrier {
		keys = append(keys, key)
	}
	return keys
}
