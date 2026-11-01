// Package store persists a workspace as git-friendly YAML files:
//
//	<root>/pyro.yaml
//	<root>/collections/<collection>/collection.yaml
//	<root>/collections/<collection>/<folder>/folder.yaml   (optional)
//	<root>/collections/<collection>/.../<request>.req.yaml
//	<root>/environments/<name>.env.yaml
//
// Requests are addressed by their slash-separated path relative to
// collections/, without extension (e.g. "payments/orders/create-order").
package store

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pyrohq/pyro/pkg/engine/model"
)

const (
	WorkspaceFile  = "pyro.yaml"
	CollectionFile = "collection.yaml"
	FolderFile     = "folder.yaml"
	RequestExt     = ".req.yaml"
	EnvExt         = ".env.yaml"

	collectionsDir  = "collections"
	environmentsDir = "environments"
)

var (
	ErrNotFound    = errors.New("not found")
	ErrInvalidPath = errors.New("invalid path")
	ErrExists      = errors.New("already exists")
)

// Workspace is an opened workspace directory.
type Workspace struct {
	Root string
	Meta model.Workspace
}

// Init creates a new, empty workspace at root. It fails if one already exists.
func Init(root, name string) (*Workspace, error) {
	if _, err := os.Stat(filepath.Join(root, WorkspaceFile)); err == nil {
		return nil, fmt.Errorf("workspace at %s: %w", root, ErrExists)
	}
	for _, d := range []string{collectionsDir, environmentsDir} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return nil, err
		}
	}
	ws := &Workspace{Root: root, Meta: model.Workspace{Name: name, Version: 1}}
	if err := writeYAML(filepath.Join(root, WorkspaceFile), ws.Meta); err != nil {
		return nil, err
	}
	return ws, nil
}

// Open loads an existing workspace.
func Open(root string) (*Workspace, error) {
	ws := &Workspace{Root: root}
	if err := readYAML(filepath.Join(root, WorkspaceFile), &ws.Meta); err != nil {
		return nil, fmt.Errorf("open workspace %s: %w", root, err)
	}
	return ws, nil
}

// Exists reports whether root contains a workspace.
func Exists(root string) bool {
	_, err := os.Stat(filepath.Join(root, WorkspaceFile))
	return err == nil
}

// Node is an entry in the workspace tree.
type Node struct {
	Type     string     `json:"type"` // collection | folder | request
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Kind     model.Kind `json:"kind,omitempty"`
	Method   string     `json:"method,omitempty"`
	Children []*Node    `json:"children,omitempty"`
}

// Tree returns all collections with their folders and requests.
// Folders sort before requests; both sort by name.
func (w *Workspace) Tree() ([]*Node, error) {
	base := filepath.Join(w.Root, collectionsDir)
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []*Node{}, nil
		}
		return nil, err
	}
	nodes := []*Node{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		n := &Node{Type: "collection", Name: e.Name(), Path: e.Name()}
		var c model.Collection
		if err := readYAML(filepath.Join(base, e.Name(), CollectionFile), &c); err == nil && c.Name != "" {
			n.Name = c.Name
		}
		if n.Children, err = w.walk(filepath.Join(base, e.Name()), e.Name()); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	sortNodes(nodes)
	return nodes, nil
}

func (w *Workspace) walk(dir, rel string) ([]*Node, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	nodes := []*Node{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		switch {
		case e.IsDir():
			n := &Node{Type: "folder", Name: name, Path: path.Join(rel, name)}
			if n.Children, err = w.walk(filepath.Join(dir, name), n.Path); err != nil {
				return nil, err
			}
			nodes = append(nodes, n)
		case strings.HasSuffix(name, RequestExt):
			p := path.Join(rel, strings.TrimSuffix(name, RequestExt))
			n := &Node{Type: "request", Name: strings.TrimSuffix(name, RequestExt), Path: p}
			var r model.Request
			if err := readYAML(filepath.Join(dir, name), &r); err == nil {
				if r.Name != "" {
					n.Name = r.Name
				}
				n.Kind, n.Method = r.Kind, r.Method
			}
			nodes = append(nodes, n)
		}
	}
	sortNodes(nodes)
	return nodes, nil
}

func sortNodes(nodes []*Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if (nodes[i].Type == "request") != (nodes[j].Type == "request") {
			return nodes[i].Type != "request"
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
}

// CreateCollection creates collections/<slug>/collection.yaml.
func (w *Workspace) CreateCollection(slug string, c model.Collection) error {
	dir, err := w.resolve(collectionsDir, slug)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("collection %q: %w", slug, ErrExists)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeYAML(filepath.Join(dir, CollectionFile), c)
}

// ReadCollection loads a collection's settings.
func (w *Workspace) ReadCollection(slug string) (*model.Collection, error) {
	file, err := w.resolve(collectionsDir, slug, CollectionFile)
	if err != nil {
		return nil, err
	}
	var c model.Collection
	if err := readYAML(file, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ReadRequest loads the request at reqPath.
func (w *Workspace) ReadRequest(reqPath string) (*model.Request, error) {
	file, err := w.requestFile(reqPath)
	if err != nil {
		return nil, err
	}
	var r model.Request
	if err := readYAML(file, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// WriteRequest creates or replaces the request at reqPath, creating folders as needed.
// The first path segment must be an existing collection.
func (w *Workspace) WriteRequest(reqPath string, r *model.Request) error {
	file, err := w.requestFile(reqPath)
	if err != nil {
		return err
	}
	coll := strings.SplitN(reqPath, "/", 2)[0]
	if _, err := os.Stat(filepath.Join(w.Root, collectionsDir, coll)); err != nil {
		return fmt.Errorf("collection %q: %w", coll, ErrNotFound)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return writeYAML(file, r)
}

// DeleteRequest removes the request at reqPath.
func (w *Workspace) DeleteRequest(reqPath string) error {
	file, err := w.requestFile(reqPath)
	if err != nil {
		return err
	}
	if err := os.Remove(file); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (w *Workspace) requestFile(reqPath string) (string, error) {
	if !strings.Contains(reqPath, "/") {
		return "", fmt.Errorf("%w: request path must include a collection: %q", ErrInvalidPath, reqPath)
	}
	return w.resolve(collectionsDir, reqPath+RequestExt)
}

// ListEnvironments returns environment names sorted alphabetically.
func (w *Workspace) ListEnvironments() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(w.Root, environmentsDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), EnvExt) {
			names = append(names, strings.TrimSuffix(e.Name(), EnvExt))
		}
	}
	sort.Strings(names)
	return names, nil
}

func (w *Workspace) ReadEnvironment(name string) (*model.Environment, error) {
	file, err := w.envFile(name)
	if err != nil {
		return nil, err
	}
	var env model.Environment
	if err := readYAML(file, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

func (w *Workspace) WriteEnvironment(name string, env *model.Environment) error {
	file, err := w.envFile(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return writeYAML(file, env)
}

func (w *Workspace) envFile(name string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%w: environment name %q", ErrInvalidPath, name)
	}
	return w.resolve(environmentsDir, name+EnvExt)
}

// resolve joins slash-separated parts under the workspace root and rejects
// anything that would escape it (absolute paths, "..", empty segments).
// Segments are checked before joining because path.Join would silently
// clean "a/../b" into "b".
func (w *Workspace) resolve(parts ...string) (string, error) {
	raw := strings.Join(parts, "/")
	if strings.ContainsAny(raw, "\\\x00") {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, raw)
	}
	for _, seg := range strings.Split(raw, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasSuffix(seg, ":") {
			return "", fmt.Errorf("%w: %q", ErrInvalidPath, raw)
		}
	}
	return filepath.Join(w.Root, filepath.FromSlash(raw)), nil
}

func readYAML(file string, v any) error {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", file, err)
	}
	return nil
}

// writeYAML writes atomically via a temp file + rename so a crash never
// leaves a half-written request on disk.
func writeYAML(file string, v any) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	data := buf.Bytes()
	tmp, err := os.CreateTemp(filepath.Dir(file), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), file)
}
