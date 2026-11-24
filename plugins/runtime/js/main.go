// Command jsrt is Pyro's JavaScript plugin runtime: an ordinary ABI v1 plugin
// (built with GOOS=wasip1 -buildmode=c-shared) that runs a plugin's script
// with goja. The host sends one extra op to each new instance before any
// other:
//
//	{"op":"load","entry":"plugin.js","files":{"plugin.js":"…"}} → {} | {"error":"…"}
//
// The script registers its handlers through the global pyro object (see
// api.go); describe, execute, sign and process are then answered by the Go
// SDK, which calls those handlers.
package main

import (
	"encoding/json"
	"fmt"

	pyro "github.com/codeen15/pyro/plugins/sdk/go"
)

func main() {}

func init() { pyro.HandleOp("load", handleLoad) }

// loaded is set by the first load: an instance runs one script for its life.
var loaded bool

type loadMessage struct {
	Entry string            `json:"entry"`
	Files map[string]string `json:"files"`
}

type loadReply struct {
	Error string `json:"error,omitempty"`
}

func handleLoad(raw json.RawMessage) any {
	if loaded {
		return loadReply{Error: "load: a script is already loaded in this instance"}
	}
	loaded = true
	var m loadMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return loadReply{Error: "load: " + err.Error()}
	}
	src, ok := m.Files[m.Entry]
	if m.Entry == "" || !ok {
		return loadReply{Error: fmt.Sprintf("load: no entry file %q", m.Entry)}
	}
	rt, err := newRuntime(sdkRegistry{})
	if err != nil {
		return loadReply{Error: "load: " + err.Error()}
	}
	if err := rt.load(m.Entry, src); err != nil {
		return loadReply{Error: err.Error()}
	}
	return loadReply{}
}

// registry receives the script's handlers; the SDK in production, a
// recorder in tests.
type registry interface {
	protocol(id string, f pyro.ProtocolFunc)
	auth(id string, f pyro.AuthFunc)
	processor(id string, f pyro.ProcessorFunc)
}

type sdkRegistry struct{}

func (sdkRegistry) protocol(id string, f pyro.ProtocolFunc)   { pyro.RegisterProtocol(id, f) }
func (sdkRegistry) auth(id string, f pyro.AuthFunc)           { pyro.RegisterAuth(id, f) }
func (sdkRegistry) processor(id string, f pyro.ProcessorFunc) { pyro.RegisterProcessor(id, f) }
