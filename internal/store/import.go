package store

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pyrohq/pyro/pkg/engine/importer"
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
