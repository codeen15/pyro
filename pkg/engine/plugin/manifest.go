// Package plugin runs Pyro plugins: WebAssembly modules (WASI, reactor
// mode) loaded with wazero. Plugins add request types (protocol) and auth
// types (auth). They talk to the host through ABI v1 (JSON messages in
// linear memory) and may only reach the network addresses their manifest
// declares and the user approved. See docs/plugins.md.
package plugin

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ABIVersion is the plugin ABI this host speaks.
const ABIVersion = 1

const ManifestFile = "pyro-plugin.yaml"

// Kinds a plugin may provide.
const (
	KindProtocol  = "protocol"
	KindAuth      = "auth"
	KindProcessor = "processor"
)

type Manifest struct {
	Name         string       `yaml:"name" json:"name"`
	Version      string       `yaml:"version" json:"version"`
	ABI          int          `yaml:"abi" json:"abi"`
	Title        string       `yaml:"title,omitempty" json:"title,omitempty"`
	Description  string       `yaml:"description,omitempty" json:"description,omitempty"`
	Author       string       `yaml:"author,omitempty" json:"author,omitempty"`
	License      string       `yaml:"license,omitempty" json:"license,omitempty"`
	Homepage     string       `yaml:"homepage,omitempty" json:"homepage,omitempty"`
	Module       string       `yaml:"module" json:"module"`
	Provides     []Provide    `yaml:"provides" json:"provides"`
	Capabilities Capabilities `yaml:"capabilities" json:"capabilities"`
	// Config fields are plugin-wide settings the user fills in.
	Config []Field `yaml:"config,omitempty" json:"config,omitempty"`
	Limits Limits  `yaml:"limits,omitempty" json:"limits,omitempty"`
}

// Provide is one feature: a request type or an auth type.
type Provide struct {
	Kind           string `yaml:"kind" json:"kind"`
	ID             string `yaml:"id" json:"id"`
	Title          string `yaml:"title,omitempty" json:"title,omitempty"`
	URLPlaceholder string `yaml:"urlPlaceholder,omitempty" json:"urlPlaceholder,omitempty"`
	// Fields are stored in request.options (protocol), auth.params (auth)
	// or the processor's options (processor).
	Fields []Field `yaml:"fields,omitempty" json:"fields,omitempty"`
	// Phases lists where a processor can run: pre, post (default both).
	Phases []string `yaml:"phases,omitempty" json:"phases,omitempty"`
}

// Field describes a form input the UI renders.
type Field struct {
	Key     string   `yaml:"key" json:"key"`
	Label   string   `yaml:"label,omitempty" json:"label,omitempty"`
	Type    string   `yaml:"type,omitempty" json:"type,omitempty"` // string | secret | number | boolean | select | text
	Options []string `yaml:"options,omitempty" json:"options,omitempty"`
	Default string   `yaml:"default,omitempty" json:"default,omitempty"`
	Help    string   `yaml:"help,omitempty" json:"help,omitempty"`
}

// Capabilities are what the plugin may do outside its sandbox.
type Capabilities struct {
	// Network lists host:port patterns it may connect to; * matches any
	// host part or any port, e.g. "*.example.com:443", "localhost:*", "*:*".
	Network []string `yaml:"network,omitempty" json:"network,omitempty"`
}

func (c Capabilities) Equal(o Capabilities) bool {
	if len(c.Network) != len(o.Network) {
		return false
	}
	for i := range c.Network {
		if c.Network[i] != o.Network[i] {
			return false
		}
	}
	return true
}

// Allows reports whether addr (host:port) matches a network pattern.
func (c Capabilities) Allows(addr string) bool {
	host, port, ok := splitHostPort(addr)
	if !ok {
		return false
	}
	for _, p := range c.Network {
		ph, pp, ok := splitHostPort(p)
		if !ok {
			continue
		}
		hostOK, _ := path.Match(strings.ToLower(ph), strings.ToLower(host))
		if (hostOK || ph == "*") && (pp == "*" || pp == port) {
			return true
		}
	}
	return false
}

func splitHostPort(s string) (host, port string, ok bool) {
	i := strings.LastIndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	host = strings.Trim(s[:i], "[]")
	return host, s[i+1:], true
}

// Limits bound a plugin's resources.
type Limits struct {
	MemoryMB int `yaml:"memoryMB,omitempty" json:"memoryMB,omitempty"` // default 128
}

var (
	nameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	idRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	moduleRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+\.wasm$`)
	versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$`)
)

// ParseManifest parses and validates a manifest.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", ManifestFile, err)
	}
	var problems []string
	if !nameRe.MatchString(m.Name) {
		problems = append(problems, "name must be lowercase letters, digits and dashes")
	}
	if !versionRe.MatchString(m.Version) {
		problems = append(problems, "version must look like 1.2.3")
	}
	if m.ABI != ABIVersion {
		problems = append(problems, fmt.Sprintf("abi %d is not supported (this Pyro speaks ABI %d)", m.ABI, ABIVersion))
	}
	if !moduleRe.MatchString(m.Module) {
		problems = append(problems, "module must be a .wasm file name in the plugin directory")
	}
	if len(m.Provides) == 0 {
		problems = append(problems, "provides is empty")
	}
	seen := map[string]bool{}
	for _, p := range m.Provides {
		if p.Kind != KindProtocol && p.Kind != KindAuth && p.Kind != KindProcessor {
			problems = append(problems, fmt.Sprintf("provides %q: kind must be protocol, auth or processor", p.ID))
		}
		for _, ph := range p.Phases {
			if p.Kind != KindProcessor || (ph != "pre" && ph != "post") {
				problems = append(problems, fmt.Sprintf("provides %q: phases are pre and post, for processors only", p.ID))
			}
		}
		if !idRe.MatchString(p.ID) {
			problems = append(problems, fmt.Sprintf("provides id %q is invalid", p.ID))
		}
		if seen[p.Kind+":"+p.ID] {
			problems = append(problems, fmt.Sprintf("provides %s %q twice", p.Kind, p.ID))
		}
		seen[p.Kind+":"+p.ID] = true
	}
	for _, n := range m.Capabilities.Network {
		if _, _, ok := splitHostPort(n); !ok {
			problems = append(problems, fmt.Sprintf("network capability %q must be host:port (use * for any)", n))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid %s: %s", ManifestFile, strings.Join(problems, "; "))
	}
	if m.Title == "" {
		m.Title = m.Name
	}
	if m.Limits.MemoryMB <= 0 {
		m.Limits.MemoryMB = 128
	}
	return &m, nil
}
