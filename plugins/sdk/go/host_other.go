//go:build !wasip1

package pyro

import (
	"errors"
	"time"
)

// Outside wasm (go test, go vet) host calls are unavailable.

var errNotWasm = errors.New("pyro: host functions are only available inside Pyro")

func logBytes(int, []byte)                            {}
func dial(string, bool, time.Duration) (int32, error) { return 0, errNotWasm }
func write(int32, []byte) (int, error)                { return 0, errNotWasm }
func read(int32, []byte, time.Duration) (int, error)  { return 0, errNotWasm }
func closeConn(int32)                                 {}
