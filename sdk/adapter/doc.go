// Package adapter provides the NATS transport facade used by Hearth adapters.
// It owns transport concerns only; vendor state, retries around registration,
// credentials, polling, and upstream checkpoints remain application concerns.
package adapter
