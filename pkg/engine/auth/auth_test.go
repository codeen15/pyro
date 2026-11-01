package auth

import (
	"testing"

	"github.com/pyrohq/pyro/pkg/engine/model"
)

func header(r *model.Request, k string) string {
	for _, kv := range r.Headers {
		if kv.Key == k {
			return kv.Value
		}
	}
	return ""
}

func TestEffective(t *testing.T) {
	coll := &model.Collection{Auth: &model.Auth{Type: "bearer"}}
	folder := &model.Collection{Auth: &model.Auth{Type: "basic"}}
	plain := &model.Collection{}
	own := &model.Auth{Type: "apikey"}

	if got := Effective(own, []*model.Collection{coll, folder}); got != own {
		t.Errorf("own auth should win, got %+v", got)
	}
	if got := Effective(nil, []*model.Collection{coll, folder, plain}); got != folder.Auth {
		t.Errorf("nearest ancestor should win, got %+v", got)
	}
	if got := Effective(nil, []*model.Collection{plain}); got != nil {
		t.Errorf("no auth expected, got %+v", got)
	}
}

func TestApply(t *testing.T) {
	cases := []struct {
		auth   model.Auth
		header string
		want   string
	}{
		{model.Auth{Type: "basic", Params: map[string]string{"username": "u", "password": "p"}}, "Authorization", "Basic dTpw"},
		{model.Auth{Type: "bearer", Params: map[string]string{"token": "t"}}, "Authorization", "Bearer t"},
		{model.Auth{Type: "apikey", Params: map[string]string{"key": "X-Key", "value": "k"}}, "X-Key", "k"},
		{model.Auth{Type: "oauth2", Params: map[string]string{"accessToken": "a"}}, "Authorization", "Bearer a"},
	}
	for _, c := range cases {
		r := &model.Request{}
		if err := Apply(r, &c.auth); err != nil {
			t.Fatalf("%s: %v", c.auth.Type, err)
		}
		if got := header(r, c.header); got != c.want {
			t.Errorf("%s: %s = %q, want %q", c.auth.Type, c.header, got, c.want)
		}
	}

	r := &model.Request{}
	Apply(r, &model.Auth{Type: "apikey", Params: map[string]string{"key": "k", "value": "v", "in": "query"}})
	if len(r.Query) != 1 || r.Query[0].Key != "k" {
		t.Errorf("apikey in query: %+v", r.Query)
	}

	r = &model.Request{Headers: []model.KeyValue{{Key: "authorization", Value: "mine"}}}
	Apply(r, &model.Auth{Type: "bearer", Params: map[string]string{"token": "t"}})
	if len(r.Headers) != 1 || r.Headers[0].Value != "mine" {
		t.Errorf("explicit header must not be overwritten: %+v", r.Headers)
	}

	for _, a := range []model.Auth{{Type: "oauth2"}, {Type: "hawk"}, {Type: "apikey"}} {
		if err := Apply(&model.Request{}, &a); err == nil {
			t.Errorf("%s: expected error", a.Type)
		}
	}
}
