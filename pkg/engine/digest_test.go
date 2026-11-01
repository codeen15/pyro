package engine

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol/httpdriver"
)

func md5hex(s string) string { h := md5.Sum([]byte(s)); return hex.EncodeToString(h[:]) }

var digestParam = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|([^,\s]*))`)

func TestDigestAuth(t *testing.T) {
	var mu sync.Mutex
	nonce, challenges, oks := "n1", 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		p := map[string]string{}
		for _, m := range digestParam.FindAllStringSubmatch(r.Header.Get("Authorization"), -1) {
			p[m[1]] = m[2] + m[3]
		}
		ha1 := md5hex("ada:pyro:pw")
		ha2 := md5hex(r.Method + ":" + r.URL.RequestURI())
		want := md5hex(ha1 + ":" + nonce + ":" + p["nc"] + ":" + p["cnonce"] + ":auth:" + ha2)
		if p["nonce"] != nonce || p["response"] != want || p["uri"] != r.URL.RequestURI() {
			challenges++
			stale := ""
			if p["nonce"] != "" && p["nonce"] != nonce {
				stale = ", stale=true"
			}
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Digest realm="pyro", qop="auth", nonce=%q, opaque="o"%s`, nonce, stale))
			w.WriteHeader(401)
			return
		}
		oks++
		fmt.Fprint(w, "welcome")
	}))
	defer srv.Close()

	eng := New(httpdriver.New(httpdriver.Options{}))
	req := &model.Request{Kind: model.KindHTTP, Method: "POST", URL: srv.URL + "/a", Query: []model.KeyValue{{Key: "q", Value: "1 2"}},
		Auth: &model.Auth{Type: "digest", Params: map[string]string{"username": "ada", "password": "pw"}}}
	send := func() *Result { return eng.Send(context.Background(), SendInput{Request: req}) }

	res := send()
	if res.Error != "" || res.Response.Status != 200 || challenges != 1 {
		t.Fatalf("first: %+v status %v, %d challenges", res.Error, res.Response, challenges)
	}
	// The challenge is cached: no 401 round trip now.
	if res = send(); res.Response.Status != 200 || challenges != 1 {
		t.Fatalf("second: %d, %d challenges", res.Response.Status, challenges)
	}
	// The server rotates its nonce: one stale 401, then success.
	mu.Lock()
	nonce = "n2"
	mu.Unlock()
	if res = send(); res.Response.Status != 200 || challenges != 2 || oks != 3 {
		t.Fatalf("after rotation: %d, %d challenges, %d ok", res.Response.Status, challenges, oks)
	}
	// Wrong password: the 401 comes back after one retry.
	req.Auth.Params["password"] = "nope"
	if res = send(); res.Response.Status != 401 {
		t.Fatalf("wrong password: %d", res.Response.Status)
	}
}
