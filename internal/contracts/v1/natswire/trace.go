package natswire

import (
	"context"

	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/propagation"
)

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

func InjectTrace(ctx context.Context, headers natsgo.Header) {
	propagation.TraceContext{}.Inject(ctx, headerCarrier(headers))
}

func ExtractTrace(ctx context.Context, headers natsgo.Header) context.Context {
	return propagation.TraceContext{}.Extract(ctx, headerCarrier(headers))
}
