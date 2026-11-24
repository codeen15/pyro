// Command alloc is a test plugin for the host's limits: option mb allocates
// that many megabytes and touches every page; option sleepMs first spins
// for that long.
package main

import (
	"strconv"
	"time"

	pyro "github.com/codeen15/pyro/plugins/sdk/go"
)

func init() {
	pyro.RegisterProtocol("alloc", pyro.ProtocolFunc(execute))
}

func main() {}

var keep []byte

func execute(_ *pyro.Context, req *pyro.Request) (*pyro.Response, error) {
	if ms, _ := strconv.Atoi(req.Option("sleepMs", "0")); ms > 0 {
		// Busy-wait: the host doesn't really sleep, and a loop in wasm
		// code stays interruptible by the call's deadline.
		d, start := time.Duration(ms)*time.Millisecond, time.Now()
		for time.Since(start) < d {
		}
	}
	if mb, _ := strconv.Atoi(req.Option("mb", "0")); mb > 0 {
		keep = make([]byte, mb<<20)
		for i := 0; i < len(keep); i += 4096 {
			keep[i] = 1
		}
		keep = nil
	}
	return &pyro.Response{StatusText: "OK"}, nil
}
