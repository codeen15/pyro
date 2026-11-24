package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/codeen15/pyro/pkg/engine/plugin"
)

// pluginDevCmd rebuilds and reinstalls a plugin every time its files change.
// A running `pyro serve` watching the same plugins folder picks each install
// up within about a second.
func pluginDevCmd() *cobra.Command {
	var interval time.Duration
	var once bool
	cmd := &cobra.Command{
		Use:   "dev <dir>",
		Short: "Rebuild and reinstall a plugin whenever its files change",
		Long: "Watches a plugin directory. On every change a Go plugin is built to WebAssembly (go build with\n" +
			"GOOS=wasip1) and the plugin is installed over the previous version, with its permissions approved\n" +
			"for you; a JavaScript plugin is just installed. A running `pyro serve` picks the new version up\n" +
			"within about a second. When a build or install fails the error is printed and the previous\n" +
			"version stays installed and working. Press Ctrl-C to stop.",
		Example: "  pyro plugin dev ./my-plugin\n  pyro plugin dev ./my-plugin --once",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval <= 0 {
				return errors.New("--interval must be positive")
			}
			dir := args[0]
			if st, err := os.Stat(dir); err != nil || !st.IsDir() {
				return fmt.Errorf("%s is not a directory", dir)
			}
			m, err := managerOrErr()
			if err != nil {
				return err
			}
			defer m.Close()
			out := cmd.OutOrStdout()

			if once {
				if err := runDevCycle(dir, m, out); err != nil {
					return fmt.Errorf("plugin dev: %w", err)
				}
				return nil
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
			defer stop()

			fmt.Fprintf(out, "Watching %s (Ctrl-C to stop)\n", dir)
			last, _ := devFingerprint(dir)
			_ = runDevCycle(dir, m, out) // failures are printed; keep watching
			tick := time.NewTicker(interval)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-tick.C:
				}
				fp, err := devFingerprint(dir)
				if err != nil || fp == last {
					continue
				}
				last = fp
				_ = runDevCycle(dir, m, out)
			}
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 500*time.Millisecond, "how often to look for changes")
	cmd.Flags().BoolVar(&once, "once", false, "build and install once, then exit (non-zero if it fails)")
	return cmd
}

// devFingerprint hashes the names, sizes and modification times of the files
// that make up the plugin's source: everything in dir except dot-files,
// node_modules and a Go plugin's built module (which a build rewrites).
func devFingerprint(dir string) (string, error) {
	skip := ""
	if data, err := os.ReadFile(filepath.Join(dir, plugin.ManifestFile)); err == nil {
		if man, err := plugin.ParseManifest(data); err == nil && !man.IsJS() {
			skip = filepath.Clean(man.Module)
		}
	}
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || rel == skip {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", rel, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// runDevCycle builds (Go plugins) and installs the plugin in dir, printing
// the outcome to out. On failure the error is printed too and returned; the
// installed version is only replaced by a successful install, so a failed
// build leaves the previous one working.
func runDevCycle(dir string, m *plugin.Manager, out io.Writer) error {
	data, err := os.ReadFile(filepath.Join(dir, plugin.ManifestFile))
	var man *plugin.Manifest
	if err == nil {
		man, err = plugin.ParseManifest(data)
	}
	if err != nil {
		fmt.Fprintf(out, "✗ install failed: %v\n", err)
		return err
	}

	built := ""
	if !man.IsJS() {
		start := time.Now()
		c := exec.Command("go", "build", "-buildmode=c-shared", "-o", man.Module, ".")
		c.Dir = dir
		c.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if b, err := c.CombinedOutput(); err != nil {
			fmt.Fprintf(out, "✗ build failed:\n%s", b)
			if len(b) == 0 {
				fmt.Fprintf(out, "%v\n", err)
			} else if b[len(b)-1] != '\n' {
				fmt.Fprintln(out)
			}
			return errors.New("build failed")
		}
		built = fmt.Sprintf(" (built in %.1fs)", time.Since(start).Seconds())
	}

	prev, prevErr := m.Get(man.Name)
	info, err := m.InstallDir(dir, true)
	if err != nil {
		fmt.Fprintf(out, "✗ install failed: %v\n", err)
		return err
	}
	fmt.Fprintf(out, "✓ %s %s installed%s\n", info.Name, info.Version, built)

	// Approving is automatic here; say what was approved when it's news.
	if prevErr != nil || !prev.Approved || !prev.Capabilities.Equal(info.Capabilities) || prev.Limits.MemoryMB != info.Limits.MemoryMB {
		network := "none"
		if len(info.Capabilities.Network) > 0 {
			network = strings.Join(info.Capabilities.Network, ", ")
		}
		fmt.Fprintf(out, "  approved network: %s\n", network)
		if info.Limits.MemoryMB != 0 {
			fmt.Fprintf(out, "  approved memory: %d MB\n", info.Limits.MemoryMB)
		}
	}
	return nil
}
