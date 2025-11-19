package main

import (
	"io"
	"net"
	"os"
	"time"
)

type ReadOnlyConn struct {
	reader   io.Reader
	deadline time.Time
}

// Read respects deadlines and reads from the underlying reader.
func (c *ReadOnlyConn) Read(p []byte) (int, error) {
	if !c.deadline.IsZero() && time.Now().After(c.deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	return c.reader.Read(p)
}

// Write pretends success so the TLS handshake does not fail.
func (c *ReadOnlyConn) Write(p []byte) (int, error) {
	// Discard the write, but acknowledge it so handshake continues
	return len(p), nil
}

// Close is a no-op (we do not own the underlying connection).
func (c *ReadOnlyConn) Close() error { return nil }

// DummyAddr implements net.Addr safely.
type dummyAddr struct{}

func (dummyAddr) Network() string { return "tcp" }
func (dummyAddr) String() string  { return "0.0.0.0:0" }

func (c *ReadOnlyConn) LocalAddr() net.Addr  { return dummyAddr{} }
func (c *ReadOnlyConn) RemoteAddr() net.Addr { return dummyAddr{} }

// Deadlines are stored on the wrapper object.
func (c *ReadOnlyConn) SetDeadline(t time.Time) error {
	c.deadline = t
	return nil
}

func (c *ReadOnlyConn) SetReadDeadline(t time.Time) error {
	c.deadline = t
	return nil
}

func (c *ReadOnlyConn) SetWriteDeadline(t time.Time) error {
	// Writes are no-ops so we ignore this.
	return nil
}
