// Package model defines Pyro's protocol-agnostic data model: workspaces,
// collections, requests and environments. The same types are used by the
// interactive sender, the collection runner and the load engine.
package model

// Kind identifies which protocol driver executes a request.
// Plugin protocols use the form "plugin:<name>".
type Kind string

const (
	KindHTTP      Kind = "http"
	KindWebSocket Kind = "websocket"
	KindSocketIO  Kind = "socketio"
)

// Workspace is the root metadata stored in pyro.yaml.
type Workspace struct {
	Name    string `yaml:"name" json:"name"`
	Version int    `yaml:"version" json:"version"`
	// LoadAllowHosts lists hosts load tests may target without confirmation
	// (loopback is always allowed).
	LoadAllowHosts []string `yaml:"loadAllowHosts,omitempty" json:"loadAllowHosts,omitempty"`
	// AllowFilesOutsideWorkspace lets request bodies read files outside the
	// workspace directory (off by default, like Postman).
	AllowFilesOutsideWorkspace bool `yaml:"allowFilesOutsideWorkspace,omitempty" json:"allowFilesOutsideWorkspace,omitempty"`
}

// Collection is stored as collection.yaml at the root of a collection directory.
// Folders may carry the same settings in an optional folder.yaml.
type Collection struct {
	Name        string     `yaml:"name" json:"name"`
	Description string     `yaml:"description,omitempty" json:"description,omitempty"`
	Variables   []Variable `yaml:"variables,omitempty" json:"variables,omitempty"`
	Auth        *Auth      `yaml:"auth,omitempty" json:"auth,omitempty"`
	Scripts     *Scripts   `yaml:"scripts,omitempty" json:"scripts,omitempty"`
	// Order lists child file/folder names (without .req.yaml) in run order.
	// Children not listed follow, folders first, sorted by name.
	Order []string `yaml:"order,omitempty" json:"order,omitempty"`
}

// Request is one saved request, stored as <name>.req.yaml.
type Request struct {
	Kind        Kind   `yaml:"kind" json:"kind"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	Method  string     `yaml:"method,omitempty" json:"method,omitempty"`
	URL     string     `yaml:"url" json:"url"`
	Query   []KeyValue `yaml:"query,omitempty" json:"query,omitempty"`
	Headers []KeyValue `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body    *Body      `yaml:"body,omitempty" json:"body,omitempty"`

	// Auth is nil when the request inherits auth from its folder/collection.
	Auth    *Auth    `yaml:"auth,omitempty" json:"auth,omitempty"`
	Scripts *Scripts `yaml:"scripts,omitempty" json:"scripts,omitempty"`

	Assertions []Assertion `yaml:"assertions,omitempty" json:"assertions,omitempty"`
	Extract    []Extractor `yaml:"extract,omitempty" json:"extract,omitempty"`

	// Options holds settings for plugin request types (kind plugin:<id>),
	// as declared by the plugin's manifest fields.
	Options map[string]string `yaml:"options,omitempty" json:"options,omitempty"`

	// Examples are saved responses, for documentation and sharing.
	Examples []Example `yaml:"examples,omitempty" json:"examples,omitempty"`

	// Streaming protocols (WebSocket, Socket.IO).
	Messages []Message       `yaml:"messages,omitempty" json:"messages,omitempty"`
	SocketIO *SocketIOConfig `yaml:"socketio,omitempty" json:"socketio,omitempty"`
}

// KeyValue is an ordered, toggleable pair (headers, query params, form fields).
type KeyValue struct {
	Key      string `yaml:"key" json:"key"`
	Value    string `yaml:"value" json:"value"`
	Disabled bool   `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	// File marks a multipart field whose Value is a file path (relative to
	// the workspace root, or absolute).
	File bool `yaml:"file,omitempty" json:"file,omitempty"`
}

// Variable is a collection- or environment-scoped variable.
type Variable struct {
	Key      string `yaml:"key" json:"key"`
	Value    string `yaml:"value" json:"value"`
	Secret   bool   `yaml:"secret,omitempty" json:"secret,omitempty"`
	Disabled bool   `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

// BodyType values.
const (
	BodyNone       = "none"
	BodyJSON       = "json"
	BodyText       = "text"
	BodyXML        = "xml"
	BodyForm       = "form"      // application/x-www-form-urlencoded
	BodyMultipart  = "multipart" // multipart/form-data
	BodyGraphQL    = "graphql"
	BodyBinaryFile = "file"
)

type Body struct {
	Type    string     `yaml:"type" json:"type"`
	Content string     `yaml:"content,omitempty" json:"content,omitempty"`
	Fields  []KeyValue `yaml:"fields,omitempty" json:"fields,omitempty"`
	File    string     `yaml:"file,omitempty" json:"file,omitempty"`
	// GraphQL variables as a JSON string.
	Variables string `yaml:"variables,omitempty" json:"variables,omitempty"`
}

// Auth types: none, basic, bearer, apikey, oauth2, digest, or plugin:<name>.
// Params holds type-specific settings (e.g. username/password, token, key/value/in).
type Auth struct {
	Type   string            `yaml:"type" json:"type"`
	Params map[string]string `yaml:"params,omitempty" json:"params,omitempty"`
}

// Scripts are pm.*-compatible JavaScript snippets.
type Scripts struct {
	Pre  string `yaml:"pre,omitempty" json:"pre,omitempty"`
	Post string `yaml:"post,omitempty" json:"post,omitempty"`
}

// Assertion is a declarative check evaluated after a response arrives.
// Type is one of: status, header, jsonpath, body, duration.
type Assertion struct {
	Type  string `yaml:"type" json:"type"`
	Path  string `yaml:"path,omitempty" json:"path,omitempty"`
	Op    string `yaml:"op,omitempty" json:"op,omitempty"` // eq, neq, lt, gt, contains, exists, matches
	Value string `yaml:"value,omitempty" json:"value,omitempty"`
}

// Extractor copies part of a response into a variable.
// From is one of: jsonpath, header, regex, status.
type Extractor struct {
	Var  string `yaml:"var" json:"var"`
	From string `yaml:"from" json:"from"`
	Path string `yaml:"path,omitempty" json:"path,omitempty"`
}

// Example is a saved response (Postman calls these examples).
type Example struct {
	Name    string     `yaml:"name" json:"name"`
	Status  int        `yaml:"status,omitempty" json:"status,omitempty"`
	Headers []KeyValue `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body    string     `yaml:"body,omitempty" json:"body,omitempty"`
}

// Message is a saved message template for WebSocket or Socket.IO.
// For Socket.IO, Event names the event and Content holds JSON-encoded args.
type Message struct {
	Name    string `yaml:"name,omitempty" json:"name,omitempty"`
	Event   string `yaml:"event,omitempty" json:"event,omitempty"`
	Content string `yaml:"content" json:"content"`
	Binary  bool   `yaml:"binary,omitempty" json:"binary,omitempty"`
	Ack     bool   `yaml:"ack,omitempty" json:"ack,omitempty"`
}

type SocketIOConfig struct {
	Path      string   `yaml:"path,omitempty" json:"path,omitempty"` // default /socket.io/
	Namespace string   `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	AuthJSON  string   `yaml:"auth,omitempty" json:"auth,omitempty"` // CONNECT packet payload
	Listen    []string `yaml:"listen,omitempty" json:"listen,omitempty"`
}

// Environment is stored as environments/<name>.env.yaml.
type Environment struct {
	Name      string     `yaml:"name" json:"name"`
	Variables []Variable `yaml:"variables,omitempty" json:"variables,omitempty"`
}
