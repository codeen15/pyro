package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/codeen15/pyro/internal/store"
	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/metrics"
	"github.com/codeen15/pyro/pkg/engine/output"
	"github.com/codeen15/pyro/pkg/engine/report"
)

func loadCmd() *cobra.Command {
	var (
		wsDir, envName, jsonOut, htmlOut string
		agentToken                       string
		agents, outs                     []string
		maxVUs                           int
		maxRPS                           float64
		yes, insecure, quiet             bool
		agentInsecure                    bool
		agentListen                      string
		waitAgents                       int
		waitTimeout                      time.Duration
	)
	cmd := &cobra.Command{
		Use:   "load <load-test | file.load.yaml>",
		Short: "Run a load test (stress, spike, soak, concurrency)",
		Long: "Runs a load test from loadtests/<name>.load.yaml (or a plan file) against the\n" +
			"workspace's requests. Exits 1 unless every threshold passes.\n\n" +
			"Before sending traffic to hosts that are not loopback or listed under\n" +
			"loadAllowHosts in pyro.yaml, Pyro asks for confirmation (--yes skips it).",
		Example: "  pyro load checkout-stress -e staging --html report.html\n  pyro load ./plans/smoke.load.yaml --yes\n" +
			"  pyro load checkout-stress -o influxdb=http://localhost:8086/write?db=pyro -o csv=ticks.csv\n" +
			"  pyro load checkout-stress --agent 10.0.0.5:7979 --agent 10.0.0.6:7979 --agent-token s3cret",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(wsDir)
			if err != nil {
				return err
			}
			plan, err := readPlan(ws, args[0])
			if err != nil {
				return err
			}
			if envName == "" {
				envName = plan.Environment
			}
			opts := load.Options{
				Resolve: ws.Target, LoadData: ws.LoadData, MaxVUs: maxVUs, FileRoot: ws.Root, AllowFilesOutsideRoot: ws.Meta.AllowFilesOutsideWorkspace, Globals: ws.GlobalVars(), AllowHosts: ws.Meta.LoadAllowHosts, InsecureTLS: insecure,
				MaxRPS: effectiveCap(ws.Meta.LoadMaxRPS, maxRPS),
			}
			if opts.Plugins = openPlugins(); opts.Plugins != nil {
				defer opts.Plugins.Close()
			}
			if envName != "" {
				if opts.Environment, err = ws.ReadEnvironment(envName); err != nil {
					return fmt.Errorf("environment %q: %w", envName, err)
				}
				opts.Environment.Name = envName
			}
			if hosts := ws.UnconfirmedHosts(load.Hosts(plan, ws.Target, opts.Environment, opts.Globals)); len(hosts) > 0 && !yes {
				if err := confirmHosts(hosts); err != nil {
					return err
				}
			}

			if len(agents) > 0 {
				workers, infos, err := connectAgents(agents, agentToken, agentInsecure)
				if err != nil {
					return err
				}
				opts.Workers = workers
				for i, in := range infos {
					fmt.Printf("  agent %-24s %s, %d CPUs, %s/%s\n", workers[i].Name(), in.Name, in.CPUs, in.OS, in.Arch)
				}
			}

			sinks, err := output.OpenAll(outs, plan.Name, time.Now().Format("20060102-150405"))
			if err != nil {
				return err
			}
			if agentListen != "" {
				hub, stopHub, err := startAgentListener(agentListen, agentToken)
				if err != nil {
					return err
				}
				defer stopHub()
				n := max(waitAgents, 1)
				fmt.Printf("Waiting for %d agent(s) to connect (up to %s)\n", n, waitTimeout)
				wctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
				err = hub.Wait(wctx, n)
				cancel()
				if err != nil {
					return fmt.Errorf("only %d of %d agents connected within %s", len(hub.Connected()), n, waitTimeout)
				}
				for _, w := range hub.Claim(n) {
					opts.Workers = append(opts.Workers, w)
					fmt.Printf("  agent %s (connected)\n", w.Name())
				}
			}

			live := newLiveLine(quiet)
			var started atomic.Pointer[load.Test]
			opts.OnTick = func(t load.Tick) {
				live.tick(t)
				if len(sinks) > 0 {
					var reqs []metrics.RequestStat
					if lt := started.Load(); lt != nil {
						reqs, _ = lt.Snapshot()
					}
					sinks.Tick(t, reqs)
				}
			}
			where := ""
			if len(opts.Workers) > 0 {
				where = fmt.Sprintf(" on %d agents", len(opts.Workers))
			}
			fmt.Printf("Load test %q: %d scenario(s), up to %d VUs%s\n", plan.Name, len(plan.Scenarios), load.PeakVUs(plan), where)
			test, err := load.Start(plan, opts)
			if err != nil {
				sinks.Close()
				return err
			}
			started.Store(test)
			sigs := make(chan os.Signal, 2)
			signal.Notify(sigs, os.Interrupt)
			defer signal.Stop(sigs)
			go func() {
				<-sigs
				live.note("Stopping: letting in-flight iterations finish (Ctrl+C again to abort)…")
				test.Stop()
				<-sigs
				live.note("Aborting.")
				test.Abort()
			}()
			res := test.Wait()
			live.done()
			if err := sinks.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "Warning:", err)
			}
			printLoadSummary(res)

			if jsonOut != "" {
				b, err := report.LoadJSON(res)
				if err != nil {
					return err
				}
				if err := os.WriteFile(jsonOut, b, 0o644); err != nil {
					return err
				}
			}
			if htmlOut != "" {
				b, err := report.LoadHTML(res)
				if err != nil {
					return err
				}
				if err := os.WriteFile(htmlOut, b, 0o644); err != nil {
					return err
				}
				fmt.Printf("Report: %s\n", htmlOut)
			}
			if res.Status != load.StatusPassed {
				return errRunFailed
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&wsDir, "workspace", "w", "", "workspace directory (same default as serve)")
	f.StringVarP(&envName, "environment", "e", "", "environment (overrides the plan's)")
	f.IntVar(&maxVUs, "max-vus", load.DefaultMaxVUs, "refuse plans that need more VUs than this")
	f.Float64Var(&maxRPS, "max-rps", 0, "cap requests per second across all VUs (the plan's maxRPS and pyro.yaml's loadMaxRPS also apply; the lowest wins)")
	f.BoolVarP(&yes, "yes", "y", false, "don't ask before targeting non-local hosts")
	f.BoolVar(&insecure, "insecure", false, "skip TLS certificate verification")
	f.BoolVarP(&quiet, "quiet", "q", false, "no live progress line")
	f.StringVar(&jsonOut, "json", "", "write the full result as JSON")
	f.StringVar(&htmlOut, "html", "", "write a self-contained HTML report")
	f.StringArrayVarP(&outs, "out", "o", nil, "stream live metrics: influxdb=URL, prometheus=URL, json=FILE or csv=FILE (repeatable)")
	f.StringSliceVar(&agents, "agent", nil, "run distributed on these agents (host:port; repeat or comma-separate)")
	f.StringVar(&agentToken, "agent-token", "", "token for the agents (default: $PYRO_AGENT_TOKEN)")
	f.BoolVar(&agentInsecure, "agent-insecure", false, "skip TLS certificate verification for https agents")
	f.StringVar(&agentListen, "agent-listen", "", "accept agents that connect out (pyro agent --connect) on this address, e.g. :7980")
	f.IntVar(&waitAgents, "wait-agents", 1, "with --agent-listen: how many agents to wait for")
	f.DurationVar(&waitTimeout, "wait-timeout", 2*time.Minute, "with --agent-listen: how long to wait for them")
	return cmd
}

// readPlan accepts a load test name or a path to a plan file.
func readPlan(ws *store.Workspace, arg string) (*load.Plan, error) {
	if strings.HasSuffix(arg, ".yaml") || strings.HasSuffix(arg, ".yml") || strings.ContainsRune(arg, filepath.Separator) {
		data, err := os.ReadFile(arg)
		if err != nil {
			return nil, err
		}
		p, err := load.ParsePlan(data, ws.RequestExists)
		if err != nil {
			return nil, err
		}
		if err := load.InlineScripts(p, ws.ReadFile); err != nil {
			return nil, err
		}
		if p.Name == "" {
			p.Name = strings.TrimSuffix(filepath.Base(arg), store.LoadExt)
		}
		return p, nil
	}
	p, err := ws.LoadPlan(arg)
	if err != nil {
		return nil, fmt.Errorf("load test %q: %w", arg, err)
	}
	if p.Name == "" {
		p.Name = arg
	}
	return p, nil
}

func confirmHosts(hosts []string) error {
	msg := "This load test will send traffic to: " + strings.Join(hosts, ", ")
	if !stdinIsTerminal() {
		return fmt.Errorf("%s\nRe-run with --yes to confirm, or add the hosts to loadAllowHosts in pyro.yaml", msg)
	}
	fmt.Printf("%s\nOnly load test systems you own or are allowed to test. Continue? [y/N] ", msg)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		return fmt.Errorf("cancelled")
	}
	return nil
}

// liveLine prints one progress line per tick: rewritten in place on a
// terminal, every 10 seconds otherwise (CI logs).
type liveLine struct {
	quiet, tty bool
	last       time.Time
	shown      bool
}

func newLiveLine(quiet bool) *liveLine {
	st, err := os.Stdout.Stat()
	return &liveLine{quiet: quiet, tty: err == nil && st.Mode()&os.ModeCharDevice != 0}
}

func (l *liveLine) tick(t load.Tick) {
	if l.quiet {
		return
	}
	failing := 0
	for _, th := range t.Thresholds {
		if !th.Passed {
			failing++
		}
	}
	ths := ""
	if len(t.Thresholds) > 0 {
		ths = "  thresholds ok"
		if failing > 0 {
			ths = fmt.Sprintf("  %d threshold(s) failing", failing)
		}
	}
	line := fmt.Sprintf("%s  VUs %-5d %8.1f req/s  p95 %-8s failed %5.2f%%%s",
		clockOf(t.Elapsed), t.VUs, t.RPS, report.FormatMS(t.Latency.P95), t.ErrorRate*100, ths)
	if t.RPS == 0 && t.WSMessagesPS > 0 {
		line = fmt.Sprintf("%s  VUs %-5d %8.1f msgs/s received%s", clockOf(t.Elapsed), t.VUs, t.WSMessagesPS, ths)
	}
	if l.tty {
		fmt.Printf("\r\x1b[K%s", line)
		l.shown = true
		return
	}
	if time.Since(l.last) >= 10*time.Second {
		fmt.Println(line)
		l.last = time.Now()
	}
}

func (l *liveLine) note(msg string) {
	if l.tty && l.shown {
		fmt.Println()
	}
	fmt.Println(msg)
	l.shown = false
}

func (l *liveLine) done() {
	if l.tty && l.shown {
		fmt.Println()
	}
}

func clockOf(secs float64) string {
	s := int(secs + 0.5)
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

func printLoadSummary(r *load.Result) {
	fmt.Println()
	verdict := map[string]string{
		load.StatusPassed: "PASSED", load.StatusFailed: "FAILED", load.StatusAborted: "ABORTED", load.StatusStopped: "STOPPED",
	}[r.Status]
	fmt.Printf("%s  %s in %s, up to %d VUs", verdict, r.Name, clockOf(r.Duration), r.MaxVUs)
	if r.MaxRPS > 0 {
		fmt.Printf(", capped at %g req/s", r.MaxRPS)
	}
	if r.Reason != "" {
		fmt.Printf(" (%s)", r.Reason)
	}
	fmt.Println()
	m := r.Metrics
	d := m["http_req_duration"]
	if d.Count > 0 {
		fmt.Printf("  requests   %d (%.1f/s), %.2f%% failed\n", int64(m["http_reqs"].Value), m["http_reqs"].PerSecond, m["http_req_failed"].Value*100)
		fmt.Printf("  latency    avg %s  p50 %s  p95 %s  p99 %s  max %s\n",
			report.FormatMS(d.Avg), report.FormatMS(d.Med), report.FormatMS(d.P95), report.FormatMS(d.P99), report.FormatMS(d.Max))
	}
	if n := m["iterations"].Value; n > 0 {
		fmt.Printf("  iterations %d", int64(n))
		if dr := m["dropped_iterations"].Value; dr > 0 {
			fmt.Printf(" (%d dropped: not enough VUs for the arrival rate)", int64(dr))
		}
		fmt.Println()
	}
	if s := m["ws_sessions"].Value; s > 0 {
		fmt.Printf("  streams    %d sessions, %d messages sent, %d received\n", int64(s), int64(m["ws_msgs_sent"].Value), int64(m["ws_msgs_received"].Value))
	}
	if len(r.Requests) > 0 {
		fmt.Println("\n  Request                                   count   failed      p95      p99")
		for i, q := range r.Requests {
			if i == 15 {
				fmt.Printf("  … %d more in the report\n", len(r.Requests)-15)
				break
			}
			name := q.Method + " " + q.Request
			if len(name) > 40 {
				name = name[:39] + "…"
			}
			fmt.Printf("  %-40s %7d %8d %8s %8s\n", name, q.Count, q.Failed, report.FormatMS(q.Duration.P95), report.FormatMS(q.Duration.P99))
		}
	}
	if len(r.Agents) > 0 {
		fmt.Println("\n  Agent                                     segment requests  VUs  peak CPU  state")
		for _, a := range r.Agents {
			state := a.State
			if a.Error != "" {
				state += ": " + a.Error
			}
			fmt.Printf("  %-40s %8d %8d %4d  %7.0f%%  %s\n", a.Name, a.Segment+1, a.Requests, a.VUs, a.PeakCPU*100, state)
		}
	}
	for _, w := range r.Warnings {
		fmt.Printf("\n  Warning: %s\n", w)
	}
	if len(r.Thresholds) > 0 {
		fmt.Println("\n  Thresholds")
		for _, t := range r.Thresholds {
			mark := "✓"
			note := ""
			if t.NoData {
				mark = "-"
			} else if !t.Passed {
				mark = "✗"
			} else if t.Breached {
				note = ", failed earlier in the run"
			}
			fmt.Printf("  %s %s %s (actual %.4g%s)\n", mark, t.Metric, t.Condition, t.Actual, note)
		}
	}
	if len(r.Errors) > 0 {
		fmt.Println("\n  Errors")
		for i, e := range r.Errors {
			if i == 10 {
				break
			}
			fmt.Printf("  %6d  %s\n", e.Count, e.Message)
		}
	}
}

// effectiveCap is the lower of two caps, ignoring zeros.
func effectiveCap(a, b float64) float64 {
	switch {
	case a <= 0:
		return b
	case b <= 0 || a < b:
		return a
	}
	return b
}
