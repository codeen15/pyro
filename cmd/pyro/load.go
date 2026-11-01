package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pyrohq/pyro/internal/store"
	"github.com/pyrohq/pyro/pkg/engine/load"
	"github.com/pyrohq/pyro/pkg/engine/report"
)

func loadCmd() *cobra.Command {
	var (
		wsDir, envName, jsonOut, htmlOut string
		maxVUs                           int
		yes, insecure, quiet             bool
	)
	cmd := &cobra.Command{
		Use:   "load <load-test | file.load.yaml>",
		Short: "Run a load test (stress, spike, soak, concurrency)",
		Long: "Runs a load test from loadtests/<name>.load.yaml (or a plan file) against the\n" +
			"workspace's requests. Exits 1 unless every threshold passes.\n\n" +
			"Before sending traffic to hosts that are not loopback or listed under\n" +
			"loadAllowHosts in pyro.yaml, Pyro asks for confirmation (--yes skips it).",
		Example: "  pyro load checkout-stress -e staging --html report.html\n  pyro load ./plans/smoke.load.yaml --yes",
		Args:    cobra.ExactArgs(1),
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
				Resolve: ws.Target, LoadData: ws.LoadData, MaxVUs: maxVUs, FileRoot: ws.Root, InsecureTLS: insecure,
			}
			if envName != "" {
				if opts.Environment, err = ws.ReadEnvironment(envName); err != nil {
					return fmt.Errorf("environment %q: %w", envName, err)
				}
				opts.Environment.Name = envName
			}
			if hosts := ws.UnconfirmedHosts(load.Hosts(plan, ws.Target, opts.Environment)); len(hosts) > 0 && !yes {
				if err := confirmHosts(hosts); err != nil {
					return err
				}
			}

			live := newLiveLine(quiet)
			opts.OnTick = live.tick
			fmt.Printf("Load test %q: %d scenario(s), up to %d VUs\n", plan.Name, len(plan.Scenarios), load.PeakVUs(plan))
			test, err := load.Start(plan, opts)
			if err != nil {
				return err
			}
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
	f.BoolVarP(&yes, "yes", "y", false, "don't ask before targeting non-local hosts")
	f.BoolVar(&insecure, "insecure", false, "skip TLS certificate verification")
	f.BoolVarP(&quiet, "quiet", "q", false, "no live progress line")
	f.StringVar(&jsonOut, "json", "", "write the full result as JSON")
	f.StringVar(&htmlOut, "html", "", "write a self-contained HTML report")
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
	st, err := os.Stdin.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
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
