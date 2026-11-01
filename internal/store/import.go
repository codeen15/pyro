package store

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

// Import writes an imported bundle into the workspace. Existing collections
// and environments are never overwritten: name clashes get a numeric suffix,
// and the report is updated with the names actually used.
func (w *Workspace) Import(b *importer.Bundle) (*importer.Report, error) {
	rep := b.Report
	if b.Collection != nil {
		taken := map[string]bool{}
		entries, _ := os.ReadDir(filepath.Join(w.Root, collectionsDir))
		for _, e := range entries {
			taken[e.Name()] = true
		}
		slug := importer.Unique(b.Slug, taken)
		rep.Slug = slug
		if err := w.CreateCollection(slug, *b.Collection); err != nil {
			return nil, err
		}
		for p, f := range b.Folders {
			file, err := w.resolve(collectionsDir, slug, p, FolderFile)
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				return nil, err
			}
			if err := writeYAML(file, f); err != nil {
				return nil, err
			}
		}
		for _, r := range b.Requests {
			if err := w.WriteRequest(slug+"/"+r.Path, r.Request); err != nil {
				return nil, err
			}
		}
	}

	if len(b.Environments) > 0 {
		existing, err := w.ListEnvironments()
		if err != nil {
			return nil, err
		}
		taken := map[string]bool{}
		for _, n := range existing {
			taken[n] = true
		}
		rep.Environments = nil
		for _, env := range b.Environments {
			name := importer.Unique(envFileName(env.Name), taken)
			if err := w.WriteEnvironment(name, env); err != nil {
				return nil, err
			}
			rep.Environments = append(rep.Environments, name)
		}
	}
	return &rep, nil
}

// envFileName keeps environment names readable but file-system safe.
func envFileName(name string) string {
	if strings.ContainsAny(name, `/\:*?"<>|`) || strings.HasPrefix(name, ".") || name == "" {
		return importer.Slugify(name)
	}
	return name
}

// ImportInto adds an imported bundle's requests to an existing collection or
// folder (target, e.g. "payments" or "payments/orders") instead of creating
// a collection. Names that clash with existing children get a suffix.
// Variables the target collection lacks are added to it. The bundle's
// collection auth is copied onto requests that would have inherited it, so
// they keep working under the target's settings; collection scripts are not
// copied (reported).
func (w *Workspace) ImportInto(target string, b *importer.Bundle) (*importer.Report, error) {
	rep := b.Report
	target = strings.Trim(target, "/")
	if b.Collection == nil {
		return nil, fmt.Errorf("this file has no requests to add to %s: %w", target, ErrInvalidPath)
	}
	dir, err := w.resolve(collectionsDir, target)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("collection or folder %q: %w", target, ErrNotFound)
	}
	slug := strings.SplitN(target, "/", 2)[0]
	coll, err := w.ReadCollection(slug)
	if err != nil {
		return nil, err
	}

	// Rename top-level items that clash with what's already there.
	taken := map[string]bool{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		taken[strings.TrimSuffix(e.Name(), RequestExt)] = true
	}
	rename := map[string]string{}
	top := func(p string) string { return strings.SplitN(p, "/", 2)[0] }
	var added []string
	for _, r := range b.Requests {
		t := top(r.Path)
		if _, ok := rename[t]; !ok {
			rename[t] = importer.Unique(t, taken)
			added = append(added, rename[t])
		}
	}
	for p := range b.Folders {
		if t := top(p); rename[t] == "" {
			rename[t] = importer.Unique(t, taken)
		}
	}
	move := func(p string) string {
		t, rest, _ := strings.Cut(p, "/")
		out := target + "/" + rename[t]
		if rest != "" {
			out += "/" + rest
		}
		return out
	}

	for p, f := range b.Folders {
		file, err := w.resolve(collectionsDir, move(p), FolderFile)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return nil, err
		}
		if err := writeYAML(file, f); err != nil {
			return nil, err
		}
	}
	for _, r := range b.Requests {
		req := r.Request
		if req.Auth == nil && b.Collection.Auth != nil && !inheritsAuth(r.Path, b.Folders) {
			req.Auth = b.Collection.Auth
		}
		if err := w.WriteRequest(move(r.Path), req); err != nil {
			return nil, err
		}
	}

	// Merge variables and keep any explicit order.
	have := map[string]bool{}
	for _, v := range coll.Variables {
		have[v.Key] = true
	}
	changed := false
	for _, v := range b.Collection.Variables {
		if !have[v.Key] {
			coll.Variables = append(coll.Variables, v)
			changed = true
		}
	}
	if target == slug && len(coll.Order) > 0 {
		coll.Order = append(coll.Order, added...)
		changed = true
	}
	if changed {
		if err := w.WriteCollection(slug, coll); err != nil {
			return nil, err
		}
	}
	if target != slug {
		file, err := w.resolve(collectionsDir, target, FolderFile)
		if err != nil {
			return nil, err
		}
		var f model.Collection
		if err := readYAML(file, &f); err == nil && len(f.Order) > 0 {
			f.Order = append(f.Order, added...)
			if err := writeYAML(file, &f); err != nil {
				return nil, err
			}
		}
	}
	if s := b.Collection.Scripts; s != nil && (s.Pre != "" || s.Post != "") {
		rep.Warn("", "import.into-scripts", "the imported collection's scripts weren't copied; add them to "+target+" if needed")
	}
	rep.Slug, rep.Into = target, true
	return &rep, nil
}

// inheritsAuth reports whether a request gets auth from one of its imported
// folders rather than from the collection.
func inheritsAuth(p string, folders map[string]*model.Collection) bool {
	for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if f := folders[dir]; f != nil && f.Auth != nil {
			return true
		}
	}
	return false
}
