package main

import (
	"io"
	"net"
	"time"
)

type ReadOnlyConn struct {
	reader io.Reader
}

func (conn ReadOnlyConn) Read(p []byte) (int, error)         { return conn.reader.Read(p) }
func (conn ReadOnlyConn) Write(p []byte) (int, error)        { return 0, io.ErrClosedPipe }
func (conn ReadOnlyConn) Close() error                       { return nil }
func (conn ReadOnlyConn) LocalAddr() net.Addr                { return nil }
func (conn ReadOnlyConn) RemoteAddr() net.Addr               { return nil }
func (conn ReadOnlyConn) SetDeadline(t time.Time) error      { return nil }
func (conn ReadOnlyConn) SetReadDeadline(t time.Time) error  { return nil }
func (conn ReadOnlyConn) SetWriteDeadline(t time.Time) error { return nil }
