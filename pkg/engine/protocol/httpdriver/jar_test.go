package httpdriver

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func names(cs []*http.Cookie) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name+"="+c.Value)
	}
	return strings.Join(out, " ")
}

func TestJarRFC6265(t *testing.T) {
	j := NewJar()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	j.now = func() time.Time { return now }
	u := func(s string) *url.URL { x, _ := url.Parse(s); return x }

	j.SetCookies(u("https://www.shop.test/account/login"), []*http.Cookie{
		{Name: "host", Value: "1"},                                 // host-only, path /account
		{Name: "dom", Value: "2", Domain: ".shop.test", Path: "/"}, // whole domain
		{Name: "sec", Value: "3", Path: "/", Secure: true},         // https only
		{Name: "evil", Value: "x", Domain: "other.test"},           // not ours: rejected
		{Name: "short", Value: "4", Path: "/", MaxAge: 60},         // expires
		{Name: "deep", Value: "5", Path: "/account/login/2fa"},     // longer path first
	})
	if got := names(j.Cookies(u("https://www.shop.test/account/login/2fa/x"))); got != "deep=5 host=1 dom=2 sec=3 short=4" {
		t.Errorf("all matching: %s", got)
	}
	if got := names(j.Cookies(u("http://api.shop.test/"))); got != "dom=2" {
		t.Errorf("subdomain over http: %s", got)
	}
	if got := names(j.Cookies(u("https://www.shop.test/accounts"))); strings.Contains(got, "host=") {
		t.Errorf("/accounts must not match path /account: %s", got)
	}
	now = now.Add(2 * time.Minute)
	if got := names(j.Cookies(u("https://www.shop.test/"))); strings.Contains(got, "short") {
		t.Errorf("expired cookie sent: %s", got)
	}
	j.SetCookies(u("https://www.shop.test/"), []*http.Cookie{{Name: "dom", Domain: "shop.test", Path: "/", MaxAge: -1}})
	if got := names(j.Cookies(u("https://shop.test/"))); got != "" {
		t.Errorf("deleted by Max-Age<0: %s", got)
	}
	if len(j.List()) != 3 {
		t.Errorf("list: %+v", j.List())
	}
	j.Put(Cookie{Name: "manual", Value: "m", Domain: ".api.test"})
	if got := names(j.Cookies(u("http://x.api.test/any"))); got != "manual=m" {
		t.Errorf("put: %s", got)
	}
	if n := j.Delete("www.shop.test", "", ""); n != 3 || len(j.List()) != 1 {
		t.Errorf("delete domain: %d, %+v", n, j.List())
	}
	j.SetCookies(u("http://127.0.0.1:8080/"), []*http.Cookie{{Name: "ip", Value: "1", Domain: "0.0.1"}, {Name: "ok", Value: "1"}})
	if got := names(j.Cookies(u("http://127.0.0.1/"))); got != "ok=1" {
		t.Errorf("IP host: %s", got)
	}
}
