package nats

import "time"

// CoreNATSWriteTimeout bounds one socket write on every Core NATS connection.
// Pinned nats.go v1.53.1 holds a connection's mutex across a socket write
// (natsWriter.flush is called from Conn.publish, from the flusher goroutine and
// from the ping timer, all under Conn.mu) and its default FlusherTimeout is one
// minute. An unread peer would therefore hold Conn.mu for up to a minute, and
// every synchronous Conn call and every Conn.Close that needs that mutex would
// block behind it. A bounded write turns a stalled socket into a failed write
// instead, so connection close during shutdown cannot outlast the Core
// shutdown budget. App assembly applies it to each Core connection's
// FlusherTimeout.
const CoreNATSWriteTimeout = time.Second
