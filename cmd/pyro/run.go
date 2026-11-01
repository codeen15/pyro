package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pyrohq/pyro/pkg/engine"
	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/protocol/httpdriver"
	"github.com/pyrohq/pyro/pkg/engine/report"
	"github.com/pyrohq/pyro/pkg/engine/runner"
)

// errRunFailed makes `pyro run` exit 1 without printing a usage error.
var errRunFailed = errors.New("run failed")

func runCmd() *cobra.Command {
	var (
		wsDir, envName, dataFile, junitOut, jsonOut string
		iterations                                  int
		delay, timeout                              time.Duration
		bail, insecure, quiet                       bool
	)
	cmd := &cobra.Command{
		Use:   "run <collection | folder | request>",
		Short: "Run requests as a functional test suite (for CI)",
		Long: "Runs every request under a collection or folder path in order, with scripts,\n" +
			"assertions and extractors. Exits 1 if any request or test fails.",
		Example: "  pyro run payments -e staging\n  pyro run payments/orders -d data/users.csv --junit results.xml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(wsDir)
			if err != nil {
				return err
			}
			refs, err := ws.Requests(args[0])
			if err != nil {
				return err
			}
			opts := runner.Options{Iterations: iterations, Delay: delay, Bail: bail}
			if envName != "" {
				if opts.Environment, err = ws.ReadEnvironment(envName); err != nil {
					return fmt.Errorf("environment %q: %w", envName, err)
				}
				opts.Environment.Name = envName
			}
			if dataFile != "" {
				if opts.Data, err = runner.LoadData(dataFile); err != nil {
					return err
				}
			}
			p := newPrinter(quiet)
			opts.OnResult = p.result

			items := make([]runner.Item, len(refs))
			for i, r := range refs {
				items[i] = runner.Item{Path: r.Path, Request: r.Request, Chain: r.Chain}
			}
			eng := engine.New(httpdriver.New(httpdriver.Options{FileRoot: ws.Root, Timeout: timeout, InsecureTLS: insecure}))
			if eng.Plugins = openPlugins(); eng.Plugins != nil {
				defer eng.Plugins.Close()
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			fmt.Printf("Running %s (%d requests)\n\n", args[0], len(items))
			sum := runner.Run(ctx, eng, items, opts)
			p.summary(sum)

			if junitOut != "" {
				b, err := report.JUnit(args[0], sum)
				if err != nil {
					return err
				}
				if err := os.WriteFile(junitOut, b, 0o644); err != nil {
					return err
				}
			}
			if jsonOut != "" {
				b, err := report.JSON(sum)
				if err != nil {
					return err
				}
				if err := os.WriteFile(jsonOut, b, 0o644); err != nil {
					return err
				}
			}
			if !sum.OK() || sum.Stopped == "cancelled" {
				return errRunFailed
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&wsDir, "workspace", "w", "", "workspace directory (same default as serve)")
	f.StringVarP(&envName, "environment", "e", "", "environment name")
	f.StringVarP(&dataFile, "data", "d", "", "CSV or JSON file; one iteration per row")
	f.IntVarP(&iterations, "iterations", "n", 0, "number of iterations (default: data rows, or 1)")
	f.DurationVar(&delay, "delay", 0, "wait between requests, e.g. 200ms")
	f.DurationVar(&timeout, "timeout", 30*time.Second, "per-request timeout")
	f.BoolVar(&bail, "bail", false, "stop at the first failed request")
	f.BoolVar(&insecure, "insecure", false, "skip TLS certificate verification")
	f.BoolVarP(&quiet, "quiet", "q", false, "only print failures and the summary")
	f.StringVar(&junitOut, "junit", "", "write a JUnit XML report to this file")
	f.StringVar(&jsonOut, "json", "", "write a JSON report to this file")
	return cmd
}

type printer struct {
	quiet bool
	color bool
	iter  int
}

func newPrinter(quiet bool) *printer {
	st, err := os.Stdout.Stat()
	color := err == nil && st.Mode()&os.ModeCharDevice != 0 && os.Getenv("NO_COLOR") == ""
	return &printer{quiet: quiet, color: color, iter: -1}
}

func (p *printer) paint(code, s string) string {
	if !p.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p *printer) result(ir runner.ItemResult) {
	failed := !ir.Passed()
	if p.quiet && !failed {
		return
	}
	if ir.Iteration != p.iter {
		p.iter = ir.Iteration
		if ir.Iteration > 0 || !p.quiet {
			fmt.Println(p.paint("2", fmt.Sprintf("Iteration %d", ir.Iteration+1)))
		}
	}
	if ir.Note != "" {
		fmt.Printf("  %s %s  %s\n", p.paint("2", "-"), ir.Path, p.paint("2", ir.Note))
		return
	}
	r := ir.Result
	mark := p.paint("32", "✓")
	if failed {
		mark = p.paint("31", "✗")
	}
	method := methodOf(r.Sent)
	line := fmt.Sprintf("  %s %-6s %s", mark, method, ir.Path)
	switch {
	case r.Skipped:
		line += p.paint("2", "  skipped by script")
	case r.Response != nil:
		ms := float64(r.Response.Timings.Total.Microseconds()) / 1000
		line += fmt.Sprintf("  %d  %s", r.Response.Status, p.paint("2", fmt.Sprintf("%.0f ms", ms)))
	}
	fmt.Println(line)
	if r.Error != "" {
		fmt.Printf("      %s\n", p.paint("31", r.Error))
	}
	for _, e := range r.ScriptErrors {
		fmt.Printf("      %s\n", p.paint("31", e))
	}
	for _, t := range r.Tests {
		if t.Passed {
			if !p.quiet {
				fmt.Printf("      %s %s\n", p.paint("32", "✓"), t.Name)
			}
			continue
		}
		fmt.Printf("      %s %s: %s\n", p.paint("31", "✗"), t.Name, t.Error)
	}
	for _, pr := range r.Problems {
		fmt.Printf("      %s\n", p.paint("33", "extract "+pr))
	}
}

func methodOf(r *model.Request) string {
	if r == nil {
		return "GET"
	}
	if id, ok := engine.PluginID(r.Kind); ok {
		return strings.ToUpper(id)
	}
	if r.Method == "" {
		return "GET"
	}
	return strings.ToUpper(r.Method)
}

func (p *printer) summary(s *runner.Summary) {
	fmt.Println()
	status := p.paint("32", "PASSED")
	if !s.OK() {
		status = p.paint("31", "FAILED")
	}
	fmt.Printf("%s  %d requests (%d failed), %d tests (%d failed), %d iteration(s) in %s\n",
		status, s.Requests, s.Failed, s.TestsPassed+s.TestsFailed, s.TestsFailed, s.Iterations, s.Duration.Round(time.Millisecond))
	if s.Stopped != "" {
		fmt.Println(p.paint("33", "Stopped: "+s.Stopped))
	}
}
