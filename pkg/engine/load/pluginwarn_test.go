package load

import (
	"reflect"
	"testing"

	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/plugin"
)

func capInfo(name, runtime string, max int) plugin.Info {
	in := plugin.Info{Manifest: plugin.Manifest{Name: name, Runtime: runtime}}
	in.Limits.MaxInstances = max
	return in
}

func TestPluginCapJobPlugins(t *testing.T) {
	tt := &Test{targets: map[string]Target{
		"a": {
			Request: &model.Request{
				Kind: "plugin:tcp",
				Pre:  []model.Processor{{Type: "plugin:sign"}, {Type: "script"}, {Type: "plugin:off", Disabled: true}},
				Post: []model.Processor{{Type: "plugin:scrub"}},
			},
			Chain: []*model.Collection{{Pre: []model.Processor{{Type: "plugin:sign"}}}},
		},
	}}
	want := []string{"processor:scrub", "processor:sign", "protocol:tcp"}
	if got := tt.jobPlugins(); !reflect.DeepEqual(got, want) {
		t.Fatalf("jobPlugins = %q, want %q", got, want)
	}
}

func TestPluginCapWarnings(t *testing.T) {
	js := capInfo("sign", plugin.RuntimeJS, 32)
	wasm := capInfo("tcp", plugin.RuntimeWasm, 4)
	unbounded := capInfo("hmac", plugin.RuntimeWasm, 0)
	cases := []struct {
		name  string
		infos []plugin.Info
		peak  int
		want  []string
	}{
		{"js over cap", []plugin.Info{js}, 100,
			[]string{"plugin sign is JavaScript and runs at most 32 calls at once; use a Go plugin for heavier load"}},
		{"js under cap", []plugin.Info{js}, 10, nil},
		{"js at cap", []plugin.Info{js}, 32, nil},
		{"wasm unbounded", []plugin.Info{unbounded}, 1000, nil},
		{"wasm over cap", []plugin.Info{wasm}, 10,
			[]string{"plugin tcp runs at most 4 calls at once; the test peaks at 10 VUs"}},
		{"several", []plugin.Info{js, unbounded, wasm}, 100, []string{
			"plugin sign is JavaScript and runs at most 32 calls at once; use a Go plugin for heavier load",
			"plugin tcp runs at most 4 calls at once; the test peaks at 100 VUs"}},
	}
	for _, c := range cases {
		if got := pluginCapWarnings(c.infos, c.peak); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
