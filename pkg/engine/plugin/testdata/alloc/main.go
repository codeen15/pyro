// Command alloc is a test plugin for the host's limits: option mb allocates
// that many megabytes and touches every page; option sleepMs sleeps first.
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
		time.Sleep(time.Duration(ms) * time.Millisecond)
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
