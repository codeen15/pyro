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
	// LoadMaxRPS caps every load test's requests per second (a safety
	// ceiling; a plan's maxRPS can only go lower). 0 = no cap.
	LoadMaxRPS float64 `yaml:"loadMaxRPS,omitempty" json:"loadMaxRPS,omitempty"`
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
	// Think is the default timer for every request beneath (nearest wins).
	Think *Timer `yaml:"think,omitempty" json:"think,omitempty"`
	// Pre and Post processors run around every request beneath, outer
	// levels first.
	Pre  []Processor `yaml:"pre,omitempty" json:"pre,omitempty"`
	Post []Processor `yaml:"post,omitempty" json:"post,omitempty"`
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

	// Assertions and Extract are the older form of post-processors; they
	// still run, after Post.
	Assertions []Assertion `yaml:"assertions,omitempty" json:"assertions,omitempty"`
	Extract    []Extractor `yaml:"extract,omitempty" json:"extract,omitempty"`

	// Think delays the request in load tests (and collection runs that opt
	// in), like a JMeter timer. Nil inherits the nearest folder/collection's.
	Think *Timer `yaml:"think,omitempty" json:"think,omitempty"`
	// Pre runs before the request is resolved; Post after the response.
	Pre  []Processor `yaml:"pre,omitempty" json:"pre,omitempty"`
	Post []Processor `yaml:"post,omitempty" json:"post,omitempty"`

	// Options holds settings for plugin request types (kind plugin:<id>),
	// as declared by the plugin's manifest fields.
	Options map[string]string `yaml:"options,omitempty" json:"options,omitempty"`

	// Examples are saved responses, for documentation and sharing.
	Examples []Example `yaml:"examples,omitempty" json:"examples,omitempty"`

	// Streaming protocols (WebSocket, Socket.IO).
	Messages []Message       `yaml:"messages,omitempty" json:"messages,omitempty"`
	SocketIO *SocketIOConfig `yaml:"socketio,omitempty" json:"socketio,omitempty"`

	// Socket steps: what a WebSocket or Socket.IO request does when it runs
	// (connect, the default; send; ping; close) and on which named
	// connection. Send and ping without a connection open their own,
	// just for that request. See SocketActions.
	Action     string `yaml:"action,omitempty" json:"action,omitempty"`
	Connection string `yaml:"connection,omitempty" json:"connection,omitempty"`
	// Wait is what a send waits for before the next step.
	Wait *Wait `yaml:"wait,omitempty" json:"wait,omitempty"`
	// CloseCode and CloseReason are sent by a close step (WebSocket).
	CloseCode   int    `yaml:"closeCode,omitempty" json:"closeCode,omitempty"`
	CloseReason string `yaml:"closeReason,omitempty" json:"closeReason,omitempty"`
}

// Socket actions.
const (
	ActionConnect = "connect"
	ActionSend    = "send"
	ActionPing    = "ping"
	ActionClose   = "close"
	// DefaultConnection names a connection when Connect gives none.
	DefaultConnection = "default"
)

// SocketAction is the request's action ("connect" when unset).
func (r *Request) SocketAction() string {
	if r.Action == "" {
		return ActionConnect
	}
	return r.Action
}

// Wait is a send step's stop condition. For is message (a received message
// matching every filter given), ack (the server acknowledges the last
// message sent with ack), time (listen for Timeout, then continue) or empty
// (don't wait). Timeout bounds message and ack waits (default 10s).
type Wait struct {
	For      string `yaml:"for,omitempty" json:"for,omitempty"`
	Event    string `yaml:"event,omitempty" json:"event,omitempty"` // Socket.IO event name
	Contains string `yaml:"contains,omitempty" json:"contains,omitempty"`
	Regex    string `yaml:"regex,omitempty" json:"regex,omitempty"`
	// JSONPath must exist in the message (equal Equals, when set).
	JSONPath string `yaml:"jsonpath,omitempty" json:"jsonpath,omitempty"`
	Equals   string `yaml:"equals,omitempty" json:"equals,omitempty"`
	Timeout  string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
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
// From is one of: jsonpath, header, regex, status, boundary, xpath.
// Boundary extracts the text between Left and Right.
type Extractor struct {
	Var   string `yaml:"var" json:"var"`
	From  string `yaml:"from" json:"from"`
	Path  string `yaml:"path,omitempty" json:"path,omitempty"`
	Left  string `yaml:"left,omitempty" json:"left,omitempty"`
	Right string `yaml:"right,omitempty" json:"right,omitempty"`
}

// Timer types (JMeter's constant, uniform random, Gaussian random and
// Poisson random timers).
const (
	TimerConstant = "constant"
	TimerUniform  = "uniform"
	TimerGaussian = "gaussian"
	TimerPoisson  = "poisson"
)

// Timer is think time before a request. Delay is the constant part; Range
// is the random part: uniform adds 0..Range, gaussian adds a normal
// deviation of Range, poisson adds a Poisson-distributed delay with mean
// Range. Durations are Go-style ("300ms", "1.5s"). Type "none" turns off
// an inherited timer.
type Timer struct {
	Type  string `yaml:"type" json:"type"`
	Delay string `yaml:"delay,omitempty" json:"delay,omitempty"`
	Range string `yaml:"range,omitempty" json:"range,omitempty"`
}

// Processor types. Pre: script, vars, counter, plugin:<id>. Post: script,
// extract, assert, vars, plugin:<id>.
const (
	ProcScript  = "script"
	ProcVars    = "vars"
	ProcCounter = "counter"
	ProcExtract = "extract"
	ProcAssert  = "assert"
)

// Processor is one step run before (pre) or after (post) a request, like
// JMeter's pre/post processors.
type Processor struct {
	Type     string `yaml:"type" json:"type"`
	Name     string `yaml:"name,omitempty" json:"name,omitempty"`
	Disabled bool   `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	// Script is pm.*-compatible JavaScript (type script).
	Script string `yaml:"script,omitempty" json:"script,omitempty"`
	// Vars sets each key to its value, variables resolved (type vars).
	Vars    []KeyValue `yaml:"vars,omitempty" json:"vars,omitempty"`
	Counter *Counter   `yaml:"counter,omitempty" json:"counter,omitempty"`
	Extract *Extractor `yaml:"extract,omitempty" json:"extract,omitempty"`
	Assert  *Assertion `yaml:"assert,omitempty" json:"assert,omitempty"`
	// Options holds a plugin processor's fields (type plugin:<id>).
	Options map[string]string `yaml:"options,omitempty" json:"options,omitempty"`
}

// Counter sets Var to Start on first use, then adds Step (default 1) each
// time. With End set, it wraps back to Start after passing End. Counters
// live in the run's variables, so each virtual user counts on its own.
type Counter struct {
	Var    string `yaml:"var" json:"var"`
	Start  int    `yaml:"start,omitempty" json:"start,omitempty"`
	Step   int    `yaml:"step,omitempty" json:"step,omitempty"`
	End    int    `yaml:"end,omitempty" json:"end,omitempty"`
	Format string `yaml:"format,omitempty" json:"format,omitempty"` // e.g. "%05d"
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
	// Transport is auto (WebSocket, falling back to long-polling; default),
	// websocket or polling.
	Transport string `yaml:"transport,omitempty" json:"transport,omitempty"`
}

// DataFormat says how to read a CSV data file into variables, like
// JMeter's CSV Data Set Config. With no Variables, the first line holds the
// column names. Variables names the columns in order instead; set Header
// too when the file still has a header line to skip. Map renames columns
// to variables ("" leaves a column out). JSON files (an array of objects)
// only use Map.
type DataFormat struct {
	Header    *bool             `yaml:"header,omitempty" json:"header,omitempty"`
	Variables []string          `yaml:"variables,omitempty" json:"variables,omitempty"`
	Map       map[string]string `yaml:"map,omitempty" json:"map,omitempty"`
	// Delimiter is one character; "\t" or "tab" for tab-separated files.
	Delimiter string `yaml:"delimiter,omitempty" json:"delimiter,omitempty"`
}

// Environment is stored as environments/<name>.env.yaml.
type Environment struct {
	Name      string     `yaml:"name" json:"name"`
	Variables []Variable `yaml:"variables,omitempty" json:"variables,omitempty"`
}
