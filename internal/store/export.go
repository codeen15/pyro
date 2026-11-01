package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

// Snapshot reads a whole collection for export: its settings, every
// folder's settings (by path relative to the collection) and its requests
// in run order (paths relative to the collection).
func (w *Workspace) Snapshot(slug string) (*importer.Bundle, error) {
	if strings.Contains(slug, "/") {
		return nil, fmt.Errorf("export a whole collection, not %q: %w", slug, ErrInvalidPath)
	}
	coll, err := w.ReadCollection(slug)
	if err != nil {
		return nil, err
	}
	tree, err := w.Tree()
	if err != nil {
		return nil, err
	}
	node := findNode(tree, slug)
	if node == nil {
		return nil, fmt.Errorf("collection %q: %w", slug, ErrNotFound)
	}
	b := &importer.Bundle{Collection: coll, Slug: slug, Folders: map[string]*model.Collection{}}
	var walk func(n *Node) error
	walk = func(n *Node) error {
		rel := strings.TrimPrefix(strings.TrimPrefix(n.Path, slug), "/")
		switch n.Type {
		case "request":
			r, err := w.ReadRequest(n.Path)
			if err != nil {
				return err
			}
			b.Requests = append(b.Requests, importer.Request{Path: rel, Request: r})
			return nil
		case "folder":
			f := &model.Collection{}
			file, err := w.resolve(collectionsDir, n.Path, FolderFile)
			if err != nil {
				return err
			}
			if err := readYAML(file, f); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			if f.Name == "" {
				f.Name = filepath.Base(n.Path)
			}
			b.Folders[rel] = f
		}
		for _, c := range n.Children {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	return b, walk(node)
}
