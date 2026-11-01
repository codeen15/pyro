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
