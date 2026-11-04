package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/codeen15/pyro/pkg/engine/model"
)

func TestCopyCollectionBetweenWorkspaces(t *testing.T) {
	src, _ := Init(t.TempDir(), "src")
	dst, _ := Init(t.TempDir(), "dst")
	if err := src.CreateCollection("shop", model.Collection{Name: "Shop"}); err != nil {
		t.Fatal(err)
	}
	if err := src.WriteRequest("shop/orders/create", &model.Request{Kind: model.KindHTTP, Name: "Create", Method: "POST", URL: "http://x"}); err != nil {
		t.Fatal(err)
	}
	if err := src.WriteEnvironment("staging", &model.Environment{Name: "staging", Variables: []model.Variable{{Key: "a", Value: "1"}}}); err != nil {
		t.Fatal(err)
	}

	slug, err := CopyCollection(src, "shop", dst, true)
	if err != nil || slug != "shop" {
		t.Fatalf("copy: %q %v", slug, err)
	}
	if r, err := dst.ReadRequest("shop/orders/create"); err != nil || r.Name != "Create" {
		t.Fatalf("copied request: %v %v", r, err)
	}
	if _, err := dst.ReadEnvironment("staging"); err != nil {
		t.Fatalf("environment not copied: %v", err)
	}

	// A second copy gets a new slug; copying within one workspace works too.
	if slug, err := CopyCollection(src, "shop", dst, false); err != nil || slug != "shop-2" {
		t.Fatalf("second copy: %q %v", slug, err)
	}
	if slug, err := CopyCollection(src, "shop", src, false); err != nil || slug != "shop-2" {
		t.Fatalf("copy within: %q %v", slug, err)
	}
	if _, err := CopyCollection(src, "nope", dst, false); err == nil {
		t.Fatal("copying a missing collection should fail")
	}
	if _, err := CopyCollection(src, "../x", dst, false); err == nil {
		t.Fatal("path escape should fail")
	}
	// No staging leftovers.
	entries, _ := os.ReadDir(filepath.Join(dst.Root, "collections"))
	for _, e := range entries {
		if e.Name()[0] == '.' {
			t.Errorf("leftover %s", e.Name())
		}
	}
}

func TestLoadTestsUsing(t *testing.T) {
	ws, _ := Init(t.TempDir(), "w")
	_ = ws.CreateCollection("shop", model.Collection{Name: "Shop"})
	_ = ws.WriteRequest("shop/list", &model.Request{Kind: model.KindHTTP, Name: "L", Method: "GET", URL: "http://x"})
	plan := "name: P\nscenarios:\n  s:\n    executor: constant-vus\n    vus: 1\n    duration: 1s\n    flow:\n      - loop: { count: 2, steps: [ { request: shop/list } ] }\n"
	if err := ws.WriteLoadTest("p", []byte(plan)); err != nil {
		t.Fatal(err)
	}
	if got, _ := ws.LoadTestsUsing("shop"); len(got) != 1 || got[0] != "p" {
		t.Fatalf("got %v", got)
	}
	if got, _ := ws.LoadTestsUsing("shopping"); len(got) != 0 {
		t.Fatalf("prefix match: %v", got)
	}
}

func TestZipRoundTrip(t *testing.T) {
	ws, _ := Init(t.TempDir(), "orig")
	_ = ws.CreateCollection("shop", model.Collection{Name: "Shop"})
	_ = ws.WriteRequest("shop/a", &model.Request{Kind: model.KindHTTP, Name: "A", Method: "GET", URL: "http://x"})
	var buf bytes.Buffer
	if err := ws.Zip(&buf); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "copy")
	got, err := Unzip(buf.Bytes(), dir, "Renamed")
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.Name != "Renamed" {
		t.Fatalf("name %q", got.Meta.Name)
	}
	if _, err := got.ReadRequest("shop/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Unzip([]byte("not a zip"), filepath.Join(t.TempDir(), "x"), ""); err == nil {
		t.Fatal("garbage should fail")
	}
}
