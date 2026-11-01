package store

import (
	"errors"
	"path"
	"reflect"
	"testing"

	"github.com/pyrohq/pyro/pkg/engine/importer"
	"github.com/pyrohq/pyro/pkg/engine/model"
)

func newWorkspace(t *testing.T) *Workspace {
	t.Helper()
	ws, err := Init(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.CreateCollection("payments", model.Collection{Name: "Payments API"}); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestInitAndOpen(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root, "demo"); !errors.Is(err, ErrExists) {
		t.Fatalf("second Init: got %v, want ErrExists", err)
	}
	ws, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Meta.Name != "demo" || ws.Meta.Version != 1 {
		t.Fatalf("meta = %+v", ws.Meta)
	}
}

func TestRequestRoundTrip(t *testing.T) {
	ws := newWorkspace(t)
	want := &model.Request{
		Kind:    model.KindHTTP,
		Name:    "Create order",
		Method:  "POST",
		URL:     "{{baseUrl}}/orders",
		Headers: []model.KeyValue{{Key: "Content-Type", Value: "application/json"}},
		Body:    &model.Body{Type: model.BodyJSON, Content: `{"qty":1}`},
		Auth:    &model.Auth{Type: "bearer", Params: map[string]string{"token": "{{token}}"}},
		Assertions: []model.Assertion{
			{Type: "status", Op: "eq", Value: "201"},
		},
	}
	if err := ws.WriteRequest("payments/orders/create-order", want); err != nil {
		t.Fatal(err)
	}
	got, err := ws.ReadRequest("payments/orders/create-order")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}

	if err := ws.DeleteRequest("payments/orders/create-order"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.ReadRequest("payments/orders/create-order"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: got %v, want ErrNotFound", err)
	}
}

func TestWriteRequestRequiresCollection(t *testing.T) {
	ws := newWorkspace(t)
	err := ws.WriteRequest("missing/thing", &model.Request{Kind: model.KindHTTP})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestTree(t *testing.T) {
	ws := newWorkspace(t)
	reqs := map[string]*model.Request{
		"payments/zeta":          {Kind: model.KindHTTP, Name: "Zeta", Method: "GET"},
		"payments/auth/login":    {Kind: model.KindHTTP, Name: "Login", Method: "POST"},
		"payments/realtime/feed": {Kind: model.KindSocketIO, Name: "Feed"},
	}
	for p, r := range reqs {
		if err := ws.WriteRequest(p, r); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := ws.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 1 || tree[0].Name != "Payments API" {
		t.Fatalf("collections = %+v", tree)
	}
	kids := tree[0].Children
	var order []string
	for _, k := range kids {
		order = append(order, k.Type+":"+k.Path)
	}
	want := []string{"folder:payments/auth", "folder:payments/realtime", "request:payments/zeta"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	login := kids[0].Children[0]
	if login.Name != "Login" || login.Method != "POST" || login.Kind != model.KindHTTP {
		t.Fatalf("login node = %+v", login)
	}
	if kids[1].Children[0].Kind != model.KindSocketIO {
		t.Fatalf("feed node = %+v", kids[1].Children[0])
	}
}

func TestPathTraversalRejected(t *testing.T) {
	ws := newWorkspace(t)
	bad := []string{
		"payments/../../etc/passwd",
		"../outside",
		"payments/./x",
		"payments//x",
		`payments\x`,
		"/abs/path",
		"nocollection",
	}
	for _, p := range bad {
		if _, err := ws.ReadRequest(p); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("ReadRequest(%q): got %v, want ErrInvalidPath", p, err)
		}
	}
	if _, err := ws.ReadEnvironment("../x"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("ReadEnvironment traversal: got %v", err)
	}
}

func TestEnvironments(t *testing.T) {
	ws := newWorkspace(t)
	for _, n := range []string{"prod", "dev"} {
		env := &model.Environment{Name: n, Variables: []model.Variable{{Key: "baseUrl", Value: "https://" + n}}}
		if err := ws.WriteEnvironment(n, env); err != nil {
			t.Fatal(err)
		}
	}
	names, err := ws.ListEnvironments()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"dev", "prod"}) {
		t.Fatalf("names = %v", names)
	}
	env, err := ws.ReadEnvironment("dev")
	if err != nil {
		t.Fatal(err)
	}
	if env.Variables[0].Value != "https://dev" {
		t.Fatalf("env = %+v", env)
	}
}

func TestChain(t *testing.T) {
	ws := newWorkspace(t)
	if err := ws.WriteRequest("payments/a/b/req", &model.Request{Kind: model.KindHTTP}); err != nil {
		t.Fatal(err)
	}
	if err := writeYAML(ws.Root+"/collections/payments/a/folder.yaml", model.Collection{Auth: &model.Auth{Type: "basic"}}); err != nil {
		t.Fatal(err)
	}
	chain, err := ws.Chain("payments/a/b/req")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 3 || chain[0].Name != "Payments API" || chain[1].Auth == nil || chain[1].Auth.Type != "basic" || chain[2].Auth != nil {
		t.Fatalf("chain = %+v", chain)
	}
	if _, err := ws.Chain("payments/../x"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("traversal: %v", err)
	}
}

func TestImportNeverOverwrites(t *testing.T) {
	ws := newWorkspace(t)
	ws.WriteEnvironment("Staging", &model.Environment{Name: "Staging"})
	b := &importer.Bundle{
		Collection: &model.Collection{Name: "Payments"},
		Slug:       "payments", // clashes with the collection from newWorkspace
		Folders:    map[string]*model.Collection{"orders": {Name: "Orders"}},
		Requests: []importer.Request{
			{Path: "orders/create", Request: &model.Request{Kind: model.KindHTTP, Name: "Create"}},
		},
		Environments: []*model.Environment{{Name: "Staging"}, {Name: "a/b"}},
	}
	rep, err := ws.Import(b)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Slug != "payments-2" || !reflect.DeepEqual(rep.Environments, []string{"Staging-2", "a-b"}) {
		t.Fatalf("report = %+v", rep)
	}
	if _, err := ws.ReadRequest("payments-2/orders/create"); err != nil {
		t.Fatal(err)
	}
	tree, _ := ws.Tree()
	for _, c := range tree {
		if c.Path == "payments-2" && c.Children[0].Name != "Orders" {
			t.Fatalf("folder display name = %q", c.Children[0].Name)
		}
	}
}

func TestOrderAndRequests(t *testing.T) {
	ws := newWorkspace(t)
	for _, p := range []string{"payments/zeta", "payments/alpha", "payments/b/two", "payments/b/one"} {
		ws.WriteRequest(p, &model.Request{Kind: model.KindHTTP, Name: path.Base(p)})
	}
	c, _ := ws.ReadCollection("payments")
	c.Order = []string{"zeta", "b"}
	ws.WriteCollection("payments", c)
	writeYAML(ws.Root+"/collections/payments/b/folder.yaml", model.Collection{Order: []string{"two", "one"}})

	refs, err := ws.Requests("payments")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range refs {
		got = append(got, r.Path)
	}
	want := []string{"payments/zeta", "payments/b/two", "payments/b/one", "payments/alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if refs, _ := ws.Requests("payments/b/one"); len(refs) != 1 || len(refs[0].Chain) != 2 {
		t.Fatalf("single request: %+v", refs)
	}
	if _, err := ws.Requests("payments/nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestLoadTests(t *testing.T) {
	ws := newWorkspace(t)
	ws.WriteRequest("payments/list", &model.Request{Kind: model.KindHTTP, Name: "List", URL: "http://127.0.0.1/x"})
	good := []byte("# smoke test\nscenarios:\n  s: { executor: constant-vus, vus: 1, duration: 1s, flow: [ { request: payments/list } ] }\n")
	if err := ws.WriteLoadTest("smoke", good); err != nil {
		t.Fatal(err)
	}
	raw, _ := ws.ReadLoadTest("smoke")
	if string(raw) != string(good) {
		t.Fatal("raw YAML (with comments) must round-trip")
	}
	var ve *ValidationError
	if err := ws.WriteLoadTest("bad", []byte("scenarios:\n  s: { executor: constant-vus, vus: 1, duration: 1s, flow: [ { request: nope/x } ] }\n")); !errors.As(err, &ve) {
		t.Fatalf("bad plan: %v", err)
	}
	if names, _ := ws.ListLoadTests(); !reflect.DeepEqual(names, []string{"smoke"}) {
		t.Fatalf("names = %v", names)
	}
	if _, err := ws.ReadLoadTest("../x"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("traversal: %v", err)
	}
	if _, err := ws.LoadData("../../etc/passwd"); err == nil {
		t.Fatal("data outside the workspace must be refused")
	}
	ws.Meta.LoadAllowHosts = []string{"staging.example.com"}
	if got := ws.UnconfirmedHosts([]string{"127.0.0.1", "localhost", "staging.example.com", "api.example.com", "::1"}); !reflect.DeepEqual(got, []string{"api.example.com"}) {
		t.Fatalf("unconfirmed = %v", got)
	}
	if err := ws.DeleteLoadTest("smoke"); err != nil {
		t.Fatal(err)
	}
}

func TestImportInto(t *testing.T) {
	ws := newWorkspace(t)
	ws.WriteRequest("payments/list", &model.Request{Kind: model.KindHTTP, Name: "List"})
	c, _ := ws.ReadCollection("payments")
	c.Variables = []model.Variable{{Key: "baseUrl", Value: "https://keep.test"}}
	c.Order = []string{"list"}
	ws.WriteCollection("payments", c)

	bearer := &model.Auth{Type: "bearer", Params: map[string]string{"token": "{{t}}"}}
	b := &importer.Bundle{
		Collection: &model.Collection{Name: "API", Auth: bearer, Variables: []model.Variable{{Key: "baseUrl", Value: "https://new.test"}, {Key: "t"}}},
		Folders:    map[string]*model.Collection{"admin": {Name: "Admin", Auth: &model.Auth{Type: "none"}}},
		Requests: []importer.Request{
			{Path: "list", Request: &model.Request{Kind: model.KindHTTP, Name: "List again"}},
			{Path: "admin/purge", Request: &model.Request{Kind: model.KindHTTP, Name: "Purge"}},
		},
		Report: importer.Report{Format: "openapi-3.0.0"},
	}
	rep, err := ws.ImportInto("payments", b)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Slug != "payments" {
		t.Errorf("slug %q", rep.Slug)
	}
	moved, err := ws.ReadRequest("payments/list-2")
	if err != nil || moved.Auth == nil || moved.Auth.Type != "bearer" {
		t.Fatalf("list-2: %+v %v (should carry the bundle's collection auth)", moved, err)
	}
	purge, err := ws.ReadRequest("payments/admin/purge")
	if err != nil || purge.Auth != nil {
		t.Fatalf("purge inherits its folder's auth: %+v %v", purge, err)
	}
	c, _ = ws.ReadCollection("payments")
	if len(c.Variables) != 2 || c.Variables[0].Value != "https://keep.test" || c.Variables[1].Key != "t" {
		t.Errorf("variables %+v", c.Variables)
	}
	if !reflect.DeepEqual(c.Order, []string{"list", "list-2", "admin"}) {
		t.Errorf("order %v", c.Order)
	}
	if _, err := ws.ImportInto("nope", b); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing target: %v", err)
	}
	if _, err := ws.ImportInto("payments", &importer.Bundle{Environments: []*model.Environment{{Name: "x"}}}); err == nil {
		t.Error("environment-only bundle accepted")
	}
}
