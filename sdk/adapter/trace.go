package adapter

import natsgo "github.com/nats-io/nats.go"

type headerCarrier natsgo.Header

func (carrier headerCarrier) Get(key string) string {
	return natsgo.Header(carrier).Get(key)
}

func (carrier headerCarrier) Set(key, value string) {
	natsgo.Header(carrier).Set(key, value)
}

func (carrier headerCarrier) Keys() []string {
	keys := make([]string, 0, len(carrier))
	for key := range carrier {
		keys = append(keys, key)
	}
	return keys
}
