// Command dist builds release archives into dist/:
//
//	go run ./scripts/dist -version v0.1.0
//
// For each target it cross-compiles bin/pyro (CGO off, version stamped),
// and packs it with LICENSE, NOTICE, README.md and THIRD_PARTY_LICENSES.txt
// as pyro_<version>_<os>_<arch>.tar.gz (.zip for Windows). It also packs the
// example plugins and writes SHA256SUMS. The web UI must be built first
// (make ui), since it is embedded.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

var targets = []struct{ os, arch string }{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

const module = "github.com/codeen15/pyro"

func main() {
	version := flag.String("version", "", "version to stamp, e.g. v0.1.0 (default: git describe)")
	out := flag.String("out", "dist", "output directory")
	only := flag.String("only", "", "build only this os/arch (e.g. linux/amd64), for testing")
	flag.Parse()
	if err := run(*version, *out, *only); err != nil {
		fmt.Fprintln(os.Stderr, "dist:", err)
		os.Exit(1)
	}
}

func run(version, out, only string) error {
	if version == "" {
		v, err := git("describe", "--tags", "--always", "--dirty")
		if err != nil {
			return fmt.Errorf("no -version and git describe failed: %w", err)
		}
		version = v
	}
	commit, _ := git("rev-parse", "--short", "HEAD")
	if _, err := os.Stat("web/dist/index.html"); err != nil {
		return fmt.Errorf("web/dist/index.html is missing: run make ui first")
	}
	for _, f := range []string{"LICENSE", "NOTICE", "README.md"} {
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("%s is missing", f)
		}
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	fmt.Println("collecting third-party licenses")
	licenses, err := thirdParty()
	if err != nil {
		return err
	}
	ver := strings.TrimPrefix(version, "v")
	ldflags := fmt.Sprintf("-s -w -X %s/internal/version.Version=%s -X %s/internal/version.Commit=%s", module, version, module, commit)
	var assets []string
	for _, t := range targets {
		if only != "" && only != t.os+"/"+t.arch {
			continue
		}
		exe := "pyro"
		if t.os == "windows" {
			exe += ".exe"
		}
		bin := filepath.Join(out, "build", t.os+"_"+t.arch, exe)
		fmt.Printf("building %s/%s\n", t.os, t.arch)
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", bin, "./cmd/pyro")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+t.os, "GOARCH="+t.arch)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go build %s/%s: %w", t.os, t.arch, err)
		}
		files := []archiveFile{{exe, bin, 0o755}, {"LICENSE", "LICENSE", 0o644}, {"NOTICE", "NOTICE", 0o644}, {"README.md", "README.md", 0o644}}
		lic := filepath.Join(out, "build", "THIRD_PARTY_LICENSES.txt")
		if err := os.WriteFile(lic, licenses, 0o644); err != nil {
			return err
		}
		files = append(files, archiveFile{"THIRD_PARTY_LICENSES.txt", lic, 0o644})
		name := fmt.Sprintf("pyro_%s_%s_%s", ver, t.os, t.arch)
		var path string
		if t.os == "windows" {
			path = filepath.Join(out, name+".zip")
			err = writeZip(path, name, files)
		} else {
			path = filepath.Join(out, name+".tar.gz")
			err = writeTarGz(path, name, files)
		}
		if err != nil {
			return err
		}
		assets = append(assets, path)
	}

	// Example plugins, packed with a binary for this machine.
	host := filepath.Join(out, "build", runtime.GOOS+"_"+runtime.GOARCH, "pyro")
	if runtime.GOOS == "windows" {
		host += ".exe"
	}
	if _, err := os.Stat(host); err == nil {
		for _, p := range []string{"tcp", "hmac"} {
			dir := filepath.Join("plugins", "examples", p)
			fmt.Printf("packing plugin %s\n", p)
			wasm := exec.Command("go", "build", "-trimpath", "-buildmode=c-shared", "-ldflags=-s -w", "-o", p+".wasm", ".")
			wasm.Dir = dir
			wasm.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
			wasm.Stdout, wasm.Stderr = os.Stdout, os.Stderr
			if err := wasm.Run(); err != nil {
				return fmt.Errorf("plugin %s: %w", p, err)
			}
			archive := filepath.Join(out, fmt.Sprintf("plugin-%s_%s.pyro-plugin", p, ver))
			pack := exec.Command(host, "plugin", "pack", dir, "-o", archive)
			pack.Stdout = io.Discard
			pack.Stderr = os.Stderr
			if err := pack.Run(); err != nil {
				return fmt.Errorf("packing %s: %w", p, err)
			}
			assets = append(assets, archive)
		}
	}

	if err := os.RemoveAll(filepath.Join(out, "build")); err != nil {
		return err
	}
	var sums strings.Builder
	for _, a := range assets {
		b, err := os.ReadFile(a)
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), filepath.Base(a))
	}
	if err := os.WriteFile(filepath.Join(out, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("\n%d assets in %s/ (version %s)\n", len(assets), out, version)
	return nil
}

func git(args ...string) (string, error) {
	b, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(b)), err
}

type archiveFile struct {
	name, src string
	mode      int64
}

// Archive entries get a fixed time so rebuilding the same commit gives
// the same bytes.
var stamp = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func writeTarGz(path, dir string, files []archiveFile) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		data, err := os.ReadFile(f.src)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: dir + "/" + f.name, Mode: f.mode, Size: int64(len(data)), ModTime: stamp, Format: tar.FormatPAX}); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func writeZip(path, dir string, files []archiveFile) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		data, err := os.ReadFile(f.src)
		if err != nil {
			return err
		}
		h := &zip.FileHeader{Name: dir + "/" + f.name, Method: zip.Deflate, Modified: stamp}
		h.SetMode(os.FileMode(f.mode))
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// thirdParty gathers the license texts of every Go module compiled into
// pyro and every npm package bundled into the web UI.
func thirdParty() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("Pyro includes the following third-party software.\n")

	out, err := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}", "./cmd/pyro").Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	seen := map[string]bool{}
	var mods []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || seen[line] || strings.HasPrefix(line, module+"\t") {
			continue
		}
		seen[line] = true
		mods = append(mods, line)
	}
	sort.Strings(mods)
	for _, m := range mods {
		parts := strings.SplitN(m, "\t", 3)
		if err := appendLicense(&b, parts[0]+" "+parts[1], parts[2]); err != nil {
			return nil, err
		}
	}
	appendFile(&b, "chai 4.5.0 (vendored)", "pkg/engine/script/js/chai-4.5.0.LICENSE")

	ls := exec.Command("npm", "ls", "--omit=dev", "--all", "--parseable")
	ls.Dir = "web"
	npmOut, err := ls.Output()
	if err != nil && len(npmOut) == 0 {
		return nil, fmt.Errorf("npm ls (run npm ci in web/ first): %w", err)
	}
	root, _ := filepath.Abs("web")
	var dirs []string
	for _, d := range strings.Split(strings.TrimSpace(string(npmOut)), "\n") {
		if d != "" && d != root {
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	done := map[string]bool{}
	for _, d := range dirs {
		var pkg struct{ Name, Version string }
		data, err := os.ReadFile(filepath.Join(d, "package.json"))
		if err != nil || json.Unmarshal(data, &pkg) != nil {
			continue
		}
		id := pkg.Name + " " + pkg.Version
		if done[id] {
			continue
		}
		done[id] = true
		if err := appendLicense(&b, "npm "+id, d); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

func appendLicense(b *bytes.Buffer, title, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	found := false
	for _, e := range entries {
		n := strings.ToUpper(e.Name())
		if !e.IsDir() && (strings.HasPrefix(n, "LICENSE") || strings.HasPrefix(n, "LICENCE") || strings.HasPrefix(n, "COPYING") || strings.HasPrefix(n, "NOTICE")) {
			appendFile(b, title+" ("+e.Name()+")", filepath.Join(dir, e.Name()))
			found = true
		}
	}
	if !found {
		// Some npm packages declare their license only in package.json.
		var pkg struct {
			License    string `json:"license"`
			Author     any    `json:"author"`
			Repository any    `json:"repository"`
		}
		if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil && json.Unmarshal(data, &pkg) == nil && pkg.License != "" {
			fmt.Fprintf(b, "\n%s\n%s\nLicense: %s (declared in package.json; the package ships no license file)\n", strings.Repeat("=", 78), title, pkg.License)
			if a := describe(pkg.Author); a != "" {
				fmt.Fprintf(b, "Author: %s\n", a)
			}
			if r := describe(pkg.Repository); r != "" {
				fmt.Fprintf(b, "Source: %s\n", r)
			}
			return nil
		}
		fmt.Fprintf(os.Stderr, "warning: no license file found for %s\n", title)
		fmt.Fprintf(b, "\n%s\n%s\n(no license file shipped with this package; see its source)\n", strings.Repeat("=", 78), title)
	}
	return nil
}

// describe flattens a package.json author or repository (a string, or an
// object with name/email or url).
func describe(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		for _, k := range []string{"url", "name"} {
			if s, ok := x[k].(string); ok && s != "" {
				if e, ok := x["email"].(string); ok && k == "name" && e != "" {
					return s + " <" + e + ">"
				}
				return s
			}
		}
	}
	return ""
}

func appendFile(b *bytes.Buffer, title, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	fmt.Fprintf(b, "\n%s\n%s\n%s\n\n%s\n", strings.Repeat("=", 78), title, strings.Repeat("-", 78), bytes.TrimSpace(data))
}
