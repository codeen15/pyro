// Package importer defines the format-neutral result that every importer
// (Postman today; OpenAPI, HAR, Insomnia later) produces.
package importer

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/pyrohq/pyro/pkg/engine/model"
)

// Bundle is an imported collection and/or environments, ready to be written
// into a workspace.
type Bundle struct {
	// Collection is nil when only environments were imported.
	Collection *model.Collection
	Slug       string
	// Folders maps a folder path (relative to the collection, slash-separated)
	// to its settings. Only folders with settings are present.
	Folders map[string]*model.Collection
	// Requests are in source order. Path is relative to the collection.
	Requests     []Request
	Environments []*model.Environment
	Report       Report
}

type Request struct {
	Path    string
	Request *model.Request
}

type Report struct {
	Format     string `json:"format"`
	Collection string `json:"collection,omitempty"`
	Slug       string `json:"slug,omitempty"`
	// Into is set when the requests were added to an existing collection or
	// folder (Slug is then its path).
	Into         bool      `json:"into,omitempty"`
	Requests     int       `json:"requests"`
	Folders      int       `json:"folders"`
	Environments []string  `json:"environments,omitempty"`
	Warnings     []Warning `json:"warnings"`
}

type Warning struct {
	Path   string `json:"path,omitempty"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (r *Report) Warn(path, code, detail string) {
	r.Warnings = append(r.Warnings, Warning{Path: path, Code: code, Detail: detail})
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a display name into a file-system-safe kebab-case name.
func Slugify(name string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 64 {
		s = strings.TrimRight(s[:64], "-")
	}
	if s == "" {
		s = "untitled"
	}
	return s
}

// Unique returns base, or base-2, base-3… — the first name not in taken.
// It records the returned name in taken.
func Unique(base string, taken map[string]bool) string {
	name := base
	for i := 2; taken[name]; i++ {
		name = base + "-" + strconv.Itoa(i)
	}
	taken[name] = true
	return name
}
