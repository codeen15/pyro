package vars

import (
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/model"
)

func TestResolveScopesAndNesting(t *testing.T) {
	r := New(
		Scope{Name: "runtime", Vars: map[string]string{"token": "rt"}},
		Scope{Name: "environment", Vars: map[string]string{"token": "env", "host": "api.dev", "baseUrl": "https://{{host}}"}},
		FromVariables("collection", []model.Variable{
			{Key: "version", Value: "v1"},
			{Key: "off", Value: "x", Disabled: true},
		}),
	)
	got, missing := r.Resolve("{{baseUrl}}/{{ version }}/users?t={{token}}&x={{off}}&y={{nope}}&z={{nope}}")
	want := "https://api.dev/v1/users?t=rt&x={{off}}&y={{nope}}&z={{nope}}"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if !reflect.DeepEqual(missing, []string{"off", "nope"}) {
		t.Fatalf("missing = %v", missing)
	}
	if _, scope, _ := r.Lookup("token"); scope != "runtime" {
		t.Fatalf("token scope = %q", scope)
	}
}

func TestSelfReferenceTerminates(t *testing.T) {
	r := New(Scope{Name: "env", Vars: map[string]string{"a": "{{b}}", "b": "{{a}}"}})
	got, _ := r.Resolve("{{a}}")
	if got != "{{a}}" && got != "{{b}}" {
		t.Fatalf("got %q", got)
	}
}

func TestDynamicVariables(t *testing.T) {
	r := New()
	r.Now = func() time.Time { return time.Unix(1700000000, 0) }
	cases := map[string]string{
		"{{$timestamp}}":    `^1700000000$`,
		"{{$isoTimestamp}}": `^2023-11-14T22:13:20\.000Z$`,
		"{{$guid}}":         `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
		"{{$randomInt}}":    `^\d{1,4}$`,
		"{{$randomEmail}}":  `^[a-z]{8}@example\.com$`,
	}
	for in, pattern := range cases {
		got, missing := r.Resolve(in)
		if len(missing) != 0 || !regexp.MustCompile(pattern).MatchString(got) {
			t.Errorf("%s -> %q (missing %v)", in, got, missing)
		}
	}
	// Unknown dynamic names fall through to normal lookup.
	if _, missing := r.Resolve("{{$unknown}}"); !reflect.DeepEqual(missing, []string{"$unknown"}) {
		t.Errorf("missing = %v", missing)
	}
}
