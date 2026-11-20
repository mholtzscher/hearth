package hap

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"net"
	"sync"
	"time"

	"github.com/brutella/hap/log"
)

type conn struct {
	net.Conn

	// s and ss are used to encrypt data. s is used to temporarily store the session.
	// After the next read, ss becomes s and the session is encrypted from then on.
	// ------------------------------------------------------------------------------------
	// 2022-02-17 (mah) This workaround is needed because switching to encryption is done
	// after sending a response. But Write() on http.ResponseWriter is not immediate.
	// So therefore we wait until the next read.
	s   *session
	smu sync.Mutex
	ss  *session

	readBuf io.Reader
	writeMu sync.Mutex
}

func newConn(c net.Conn) *conn {
	return &conn{
		Conn: c,
		smu:  sync.Mutex{},
	}
}

func (c *conn) Upgrade(s *session) {
	c.smu.Lock()
	c.s = s
	c.smu.Unlock()
}

// Write writes bytes to the connection.
// The written bytes are encrypted when possible.
func (c *conn) Write(b []byte) (int, error) {
	// Frame encryption and the socket write must stay in the same order:
	// an HTTP response and an asynchronous event may share this connection.
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.smu.Lock()
	session := c.ss
	c.smu.Unlock()
	if session == nil {
		return c.Conn.Write(b)
	}

	// Bound a stalled controller's impact on the caller publishing an event.
	const writeTimeout = 2 * time.Second
	if err := c.Conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return 0, err
	}
	defer c.Conn.SetWriteDeadline(time.Time{})
	var buf bytes.Buffer
	buf.Write(b)
	enc, err := session.Encrypt(&buf)

	if err != nil {
		log.Debug.Println("encryption failed:", err)
		err = c.Conn.Close()
		return 0, err
	}

	encB, err := ioutil.ReadAll(enc)
	if err != nil {
		return 0, err
	}
	_, err = c.Conn.Write(encB)
	if err != nil {
		c.Conn.Close()
		return 0, err
	}

	return len(b), nil
}

const (
	packetSize = 0x400
)

// Read reads bytes from the connection.
// The read bytes are decrypted when possible.
func (c *conn) Read(b []byte) (int, error) {
	c.smu.Lock()
	if c.s != nil {
		c.ss = c.s
		c.s = nil
	}
	c.smu.Unlock()

	if c.ss == nil {
		return c.Conn.Read(b)
	}

	if c.readBuf == nil {
		r := bufio.NewReader(c.Conn)
		buf, err := c.ss.Decrypt(r)
		if err != nil {
			if neterr, ok := err.(net.Error); ok && neterr.Timeout() {
				// Ignore timeout error #77
			} else if errors.Is(err, net.ErrClosed) {
				// Ignore close errors
			} else {
				log.Debug.Println("decryption failed:", err)
				c.Conn.Close()
			}
			return 0, err
		}

		c.readBuf = buf
	}

	n, err := c.readBuf.Read(b)

	if n < len(b) || err == io.EOF {
		c.readBuf = nil
	}

	return n, err
}
