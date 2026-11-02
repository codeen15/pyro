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

	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

const (
	WorkspaceFile  = "pyro.yaml"
	CollectionFile = "collection.yaml"
	FolderFile     = "folder.yaml"
	GlobalsFile    = "globals.yaml"
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
		applyOrder(n.Children, c.Order)
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
			var f model.Collection
			if err := readYAML(filepath.Join(dir, name, FolderFile), &f); err == nil && f.Name != "" {
				n.Name = f.Name
			}
			if n.Children, err = w.walk(filepath.Join(dir, name), n.Path); err != nil {
				return nil, err
			}
			applyOrder(n.Children, f.Order)
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

// applyOrder moves listed children to the front in the given order; the rest
// keep their default sort.
func applyOrder(nodes []*Node, order []string) {
	if len(order) == 0 {
		return
	}
	rank := make(map[string]int, len(order))
	for i, name := range order {
		rank[name] = i
	}
	pos := func(n *Node) int {
		if r, ok := rank[path.Base(n.Path)]; ok {
			return r
		}
		return len(order)
	}
	sort.SliceStable(nodes, func(i, j int) bool { return pos(nodes[i]) < pos(nodes[j]) })
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

// WriteCollection replaces a collection's settings.
func (w *Workspace) WriteCollection(slug string, c *model.Collection) error {
	file, err := w.resolve(collectionsDir, slug, CollectionFile)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Dir(file)); err != nil {
		return fmt.Errorf("collection %q: %w", slug, ErrNotFound)
	}
	return writeYAML(file, c)
}

// Chain returns the settings that apply to the request at reqPath, outermost
// first: the collection, then each enclosing folder. Folders without a
// folder.yaml contribute an empty entry so indexes line up with path depth.
func (w *Workspace) Chain(reqPath string) ([]*model.Collection, error) {
	if _, err := w.requestFile(reqPath); err != nil {
		return nil, err
	}
	segs := strings.Split(reqPath, "/")
	coll, err := w.ReadCollection(segs[0])
	if err != nil {
		return nil, err
	}
	chain := []*model.Collection{coll}
	for i := 2; i < len(segs); i++ {
		file, err := w.resolve(collectionsDir, strings.Join(segs[:i], "/"), FolderFile)
		if err != nil {
			return nil, err
		}
		f := &model.Collection{}
		if err := readYAML(file, f); err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		chain = append(chain, f)
	}
	return chain, nil
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

func (w *Workspace) DeleteEnvironment(name string) error {
	file, err := w.envFile(name)
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

// ReadFolder loads a folder's settings (folder.yaml); a folder without one
// has empty settings named after its directory.
func (w *Workspace) ReadFolder(p string) (*model.Collection, error) {
	p = strings.Trim(p, "/")
	if !strings.Contains(p, "/") {
		return nil, fmt.Errorf("%q is a collection, not a folder: %w", p, ErrInvalidPath)
	}
	dir, err := w.resolve(collectionsDir, p)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("folder %q: %w", p, ErrNotFound)
	}
	f := &model.Collection{}
	if err := readYAML(filepath.Join(dir, FolderFile), f); err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if f.Name == "" {
		f.Name = path.Base(p)
	}
	return f, nil
}

// WriteFolder saves a folder's settings to folder.yaml.
func (w *Workspace) WriteFolder(p string, f *model.Collection) error {
	p = strings.Trim(p, "/")
	if !strings.Contains(p, "/") {
		return fmt.Errorf("%q is a collection, not a folder: %w", p, ErrInvalidPath)
	}
	dir, err := w.resolve(collectionsDir, p)
	if err != nil {
		return err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("folder %q: %w", p, ErrNotFound)
	}
	return writeYAML(filepath.Join(dir, FolderFile), f)
}

// CreateFolder makes a new, empty folder at p (inside an existing
// collection or folder) with its settings in folder.yaml.
func (w *Workspace) CreateFolder(p string, f model.Collection) error {
	p = strings.Trim(p, "/")
	if !strings.Contains(p, "/") {
		return fmt.Errorf("%q is a collection, not a folder: %w", p, ErrInvalidPath)
	}
	dir, err := w.resolve(collectionsDir, p)
	if err != nil {
		return err
	}
	if st, err := os.Stat(filepath.Dir(dir)); err != nil || !st.IsDir() {
		return fmt.Errorf("parent of %q: %w", p, ErrNotFound)
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("folder %q: %w", p, ErrExists)
	}
	if _, err := os.Stat(dir + RequestExt); err == nil {
		return fmt.Errorf("a request named %q: %w", path.Base(p), ErrExists)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return err
	}
	if f.Name == "" {
		f.Name = path.Base(p)
	}
	return writeYAML(filepath.Join(dir, FolderFile), &f)
}

// DeleteFolder removes a folder and everything in it.
func (w *Workspace) DeleteFolder(p string) error {
	p = strings.Trim(p, "/")
	if !strings.Contains(p, "/") {
		return fmt.Errorf("%q is a collection, not a folder: %w", p, ErrInvalidPath)
	}
	return w.removeDir(p)
}

// DeleteCollection removes a collection and everything in it.
func (w *Workspace) DeleteCollection(slug string) error {
	if slug == "" || strings.Contains(slug, "/") {
		return fmt.Errorf("%w: collection %q", ErrInvalidPath, slug)
	}
	return w.removeDir(slug)
}

func (w *Workspace) removeDir(p string) error {
	dir, err := w.resolve(collectionsDir, p)
	if err != nil {
		return err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("%q: %w", p, ErrNotFound)
	}
	return os.RemoveAll(dir)
}

// ReadGlobals loads globals.yaml (workspace-wide variables). A missing
// file means no globals.
func (w *Workspace) ReadGlobals() (*model.Environment, error) {
	g := &model.Environment{Name: "globals"}
	if err := readYAML(filepath.Join(w.Root, GlobalsFile), g); err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	g.Name = "globals"
	return g, nil
}

func (w *Workspace) WriteGlobals(g *model.Environment) error {
	out := *g
	out.Name = "globals"
	return writeYAML(filepath.Join(w.Root, GlobalsFile), &out)
}

// GlobalVars returns the enabled globals as a map (empty on any problem;
// a broken globals.yaml shouldn't stop requests).
func (w *Workspace) GlobalVars() map[string]string {
	g, err := w.ReadGlobals()
	if err != nil {
		return map[string]string{}
	}
	return vars.Map(g.Variables)
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
