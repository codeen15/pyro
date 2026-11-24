package load

import (
	"fmt"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/plugin"
)

// pluginCapWarnings warns about each plugin whose instance cap is below
// the test's peak VUs: its calls queue behind the cap. A cap of 0 is
// unbounded.
func pluginCapWarnings(infos []plugin.Info, peakVUs int) []string {
	var out []string
	for _, in := range infos {
		n := in.Limits.MaxInstances
		if n <= 0 || peakVUs <= n {
			continue
		}
		if in.IsJS() {
			out = append(out, fmt.Sprintf("plugin %s is JavaScript and runs at most %d calls at once; use a Go plugin for heavier load", in.Name, n))
		} else {
			out = append(out, fmt.Sprintf("plugin %s runs at most %d calls at once; the test peaks at %d VUs", in.Name, n, peakVUs))
		}
	}
	return out
}

// pluginWarnings looks up the plugins the plan uses and warns about the
// capped ones. Each plugin is named once, however many types it provides.
func (t *Test) pluginWarnings() []string {
	if t.opts.Plugins == nil || len(t.opts.Workers) > 0 {
		return nil // a distributed test's agents run the plugins
	}
	seen := map[string]bool{}
	var infos []plugin.Info
	for _, p := range t.jobPlugins() {
		kind, id, _ := strings.Cut(p, ":")
		in, ok := t.opts.Plugins.Lookup(kind, id)
		if !ok || seen[in.Name] {
			continue
		}
		seen[in.Name] = true
		infos = append(infos, in)
	}
	return pluginCapWarnings(infos, PeakVUs(t.plan))
}
