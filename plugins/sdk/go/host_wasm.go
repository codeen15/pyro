//go:build wasip1

package pyro

import (
	"errors"
	"io"
	"time"
)

func logBytes(level int, b []byte) { hostLog(uint32(level), bytesPtr(b), uint32(len(b))) }

func lastError() error {
	buf := make([]byte, 512)
	n := hostLastError(bytesPtr(buf), uint32(len(buf)))
	if n == 0 {
		return errors.New("network error")
	}
	return errors.New(string(buf[:n]))
}

func dial(addr string, useTLS bool, timeout time.Duration) (int32, error) {
	b := []byte(addr)
	t := uint32(0)
	if useTLS {
		t = 1
	}
	h := hostDial(bytesPtr(b), uint32(len(b)), t, uint32(timeout.Milliseconds()))
	if h < 0 {
		return 0, lastError()
	}
	return h, nil
}

func write(h int32, b []byte) (int, error) {
	n := hostWrite(h, bytesPtr(b), uint32(len(b)))
	if n < 0 {
		return 0, lastError()
	}
	return int(n), nil
}

// net_read returns bytes read, 0 at EOF, -2 on timeout, -1 on error.
func read(h int32, b []byte, timeout time.Duration) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n := hostRead(h, bytesPtr(b), uint32(len(b)), uint32(timeout.Milliseconds()))
	switch {
	case n == 0:
		return 0, io.EOF
	case n == -2:
		return 0, ErrTimeout
	case n < 0:
		return 0, lastError()
	}
	return int(n), nil
}

func closeConn(h int32) { hostClose(h) }
