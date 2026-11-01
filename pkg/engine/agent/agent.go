// Package agent runs segments of distributed load tests. An agent (`pyro
// agent`) listens for a controller (`pyro load --agent`, or `pyro serve`
// with agents configured), which sends each agent its share of the plan
// over a WebSocket. Agents run their segment and stream metric deltas back
// once per tick; the controller merges them, so percentiles and thresholds
// cover every sample. See docs/load-engine.md ("Distributed tests").
//
// Messages are JSON text frames {"type": …}:
//
//	controller → agent: job {job}, begin, stop
//	agent → controller: ready, report {report}, done {status}, error {error}
//
// Closing the connection aborts a running job.
package agent

import (
	"github.com/pyrohq/pyro/pkg/engine/load"
)

// Protocol is the agent protocol version; controller and agent must match.
const Protocol = 1

const protocolHeader = "Pyro-Agent-Protocol"

// Info describes an agent (GET /agent/v1/info).
type Info struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Protocol int      `json:"protocol"`
	CPUs     int      `json:"cpus"`
	OS       string   `json:"os"`
	Arch     string   `json:"arch"`
	Busy     bool     `json:"busy"`
	MaxVUs   int      `json:"maxVUs"`
	Plugins  []string `json:"plugins,omitempty"` // kind:id of ready plugin types
}

type message struct {
	Type   string       `json:"type"`
	Job    *load.Job    `json:"job,omitempty"`
	Report *load.Report `json:"report,omitempty"`
	Status string       `json:"status,omitempty"`
	Error  string       `json:"error,omitempty"`
}
