package store

import (
	"fmt"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/model"
)

// RequestRef is a request with everything needed to send it.
type RequestRef struct {
	Path    string
	Request *model.Request
	Chain   []*model.Collection
}

// Requests expands a collection, folder or single request path into its
// requests in run order (the order shown in the tree).
func (w *Workspace) Requests(p string) ([]RequestRef, error) {
	p = strings.Trim(p, "/")
	tree, err := w.Tree()
	if err != nil {
		return nil, err
	}
	node := findNode(tree, p)
	if node == nil {
		return nil, fmt.Errorf("%q: %w", p, ErrNotFound)
	}
	var paths []string
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.Type == "request" {
			paths = append(paths, n.Path)
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(node)

	refs := make([]RequestRef, 0, len(paths))
	for _, rp := range paths {
		req, err := w.ReadRequest(rp)
		if err != nil {
			return nil, err
		}
		chain, err := w.Chain(rp)
		if err != nil {
			return nil, err
		}
		refs = append(refs, RequestRef{Path: rp, Request: req, Chain: chain})
	}
	return refs, nil
}

func findNode(nodes []*Node, p string) *Node {
	for _, n := range nodes {
		if n.Path == p {
			return n
		}
		if strings.HasPrefix(p, n.Path+"/") {
			return findNode(n.Children, p)
		}
	}
	return nil
}

// SecretValues collects the values of variables marked secret that a run of
// refs could use (environment, globals, collections and folders), so
// reports can mask them.
func (w *Workspace) SecretValues(env *model.Environment, refs []RequestRef) []string {
	var out []string
	add := func(vs []model.Variable) {
		for _, v := range vs {
			if v.Secret && v.Value != "" {
				out = append(out, v.Value)
			}
		}
	}
	if env != nil {
		add(env.Variables)
	}
	if g, err := w.ReadGlobals(); err == nil {
		add(g.Variables)
	}
	seen := map[*model.Collection]bool{}
	for _, ref := range refs {
		for _, c := range ref.Chain {
			if !seen[c] {
				seen[c] = true
				add(c.Variables)
			}
		}
	}
	return out
}
