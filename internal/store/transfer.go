package store

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/load"
)

// CopyCollection copies a collection (every file under it) into dst, which
// may be the same workspace. The copy keeps the slug, or gets "-2", "-3"…
// when dst already has one by that name. With environments, environment
// files dst lacks are copied too. It returns the new slug.
func CopyCollection(src *Workspace, slug string, dst *Workspace, environments bool) (string, error) {
	from, err := src.resolve(collectionsDir, slug)
	if err != nil || strings.Contains(slug, "/") {
		return "", fmt.Errorf("%w: collection %q", ErrInvalidPath, slug)
	}
	if st, err := os.Stat(filepath.Join(from, CollectionFile)); err != nil || st.IsDir() {
		return "", fmt.Errorf("collection %q: %w", slug, ErrNotFound)
	}
	newSlug := slug
	for i := 2; ; i++ {
		to, err := dst.resolve(collectionsDir, newSlug)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(to); errors.Is(err, fs.ErrNotExist) {
			break
		}
		newSlug = fmt.Sprintf("%s-%d", slug, i)
	}
	to, _ := dst.resolve(collectionsDir, newSlug)
	// Copy into a hidden staging folder first so a failure leaves nothing
	// half-copied under collections/.
	stage, err := os.MkdirTemp(filepath.Join(dst.Root, collectionsDir), ".copy-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err := copyTree(from, stage); err != nil {
		return "", err
	}
	if err := os.Rename(stage, to); err != nil {
		return "", err
	}
	if environments {
		if err := copyMissingEnvironments(src, dst); err != nil {
			return newSlug, err
		}
	}
	return newSlug, nil
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".tmp-") {
			return nil // symlinks and leftovers aren't copied
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return writeAtomic(target, data)
	})
}

func copyMissingEnvironments(src, dst *Workspace) error {
	names, err := src.ListEnvironments()
	if err != nil {
		return err
	}
	for _, n := range names {
		if _, err := dst.ReadEnvironment(n); err == nil {
			continue
		}
		env, err := src.ReadEnvironment(n)
		if err != nil {
			return err
		}
		if err := dst.WriteEnvironment(n, env); err != nil {
			return err
		}
	}
	return nil
}

// LoadTestsUsing lists the load tests whose steps reference requests in the
// collection (they would break if it moved away).
func (w *Workspace) LoadTestsUsing(slug string) ([]string, error) {
	names, err := w.ListLoadTests()
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, n := range names {
		plan, err := w.LoadPlan(n)
		if err != nil {
			continue
		}
		if planUses(plan, slug) {
			out = append(out, n)
		}
	}
	return out, nil
}

func planUses(p *load.Plan, slug string) bool {
	for _, sc := range p.Scenarios {
		if stepsUse(sc.Flow, slug) {
			return true
		}
	}
	return false
}

func stepsUse(steps []load.Step, slug string) bool {
	for _, s := range steps {
		if s.Request == slug || strings.HasPrefix(s.Request, slug+"/") {
			return true
		}
		if stepsUse(s.Steps, slug) {
			return true
		}
		if s.Loop != nil && stepsUse(s.Loop.Steps, slug) {
			return true
		}
		if s.If != nil && (stepsUse(s.If.Then, slug) || stepsUse(s.If.Else, slug)) {
			return true
		}
		for _, c := range s.Weighted {
			if stepsUse(c.Steps, slug) {
				return true
			}
		}
	}
	return false
}

// maxZip bounds an uploaded project archive (compressed and expanded).
const maxZip = 200 << 20

// Zip writes the workspace folder as a zip archive.
func (w *Workspace) Zip(out io.Writer) error {
	zw := zip.NewWriter(out)
	err := filepath.WalkDir(w.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(w.Root, p)
		if rel == "." || !d.Type().IsRegular() && !d.IsDir() || strings.HasPrefix(d.Name(), ".tmp-") || strings.HasPrefix(d.Name(), ".copy-") {
			if d.IsDir() && rel != "." && strings.HasPrefix(d.Name(), ".copy-") {
				return filepath.SkipDir
			}
			return nil
		}
		name := filepath.ToSlash(rel)
		if d.IsDir() {
			_, err := zw.Create(name + "/")
			return err
		}
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(f, src)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

// Unzip creates a workspace at root from a zip archive made by Zip (or any
// archive of a workspace folder, optionally inside one top-level folder).
// name, when set, replaces the workspace name.
func Unzip(data []byte, root, name string) (*Workspace, error) {
	if len(data) > maxZip {
		return nil, fmt.Errorf("%w: archive larger than %d MB", ErrInvalidPath, maxZip>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: not a zip archive", ErrInvalidPath)
	}
	// Find pyro.yaml: at the root, or one folder down.
	prefix := ""
	found := false
	for _, f := range zr.File {
		n := strings.TrimPrefix(f.Name, "./")
		if n == WorkspaceFile {
			prefix, found = "", true
			break
		}
		if dir, file := path.Split(n); file == WorkspaceFile && strings.Count(dir, "/") == 1 {
			prefix, found = dir, true
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: the archive has no %s (zip a Pyro workspace folder)", ErrInvalidPath, WorkspaceFile)
	}
	if _, err := os.Stat(root); err == nil {
		return nil, fmt.Errorf("%s: %w", root, ErrExists)
	}
	stage := root + ".unzip"
	defer os.RemoveAll(stage)
	ws := &Workspace{Root: stage}
	var total int64
	for _, f := range zr.File {
		n := strings.TrimPrefix(strings.TrimPrefix(f.Name, "./"), prefix)
		if n == "" || !strings.HasPrefix(strings.TrimPrefix(f.Name, "./"), prefix) || strings.HasSuffix(n, "/") {
			continue
		}
		if f.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		target, err := ws.resolve(n)
		if err != nil {
			return nil, err
		}
		total += int64(f.UncompressedSize64)
		if total > maxZip {
			return nil, fmt.Errorf("%w: archive expands past %d MB", ErrInvalidPath, maxZip>>20)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(rc, maxZip+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return nil, err
		}
	}
	for _, d := range []string{collectionsDir, environmentsDir} {
		if err := os.MkdirAll(filepath.Join(stage, d), 0o755); err != nil {
			return nil, err
		}
	}
	opened, err := Open(stage)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	if name != "" {
		opened.Meta.Name = name
		if err := writeYAML(filepath.Join(stage, WorkspaceFile), opened.Meta); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(stage, root); err != nil {
		return nil, err
	}
	return Open(root)
}
