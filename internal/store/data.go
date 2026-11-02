package store

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/runner"
)

const dataDir = "data"

// DataFile is a test data file under data/.
type DataFile struct {
	Path string `json:"path"` // relative to the workspace root, e.g. data/users.csv
	Size int64  `json:"size"`
}

var dataExts = map[string]bool{".csv": true, ".tsv": true, ".txt": true, ".json": true}

// ListDataFiles returns the CSV, TSV, text and JSON files under data/.
func (w *Workspace) ListDataFiles() ([]DataFile, error) {
	root := filepath.Join(w.Root, dataDir)
	out := []DataFile{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root && os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".") || !dataExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(w.Root, p)
		out = append(out, DataFile{Path: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

// WriteDataFile saves a data file as data/<name>; name may contain
// sub-folders. It returns the workspace-relative path.
func (w *Workspace) WriteDataFile(name string, data []byte) (string, error) {
	name = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, "\\", "/")), "/")
	name = strings.TrimPrefix(name, dataDir+"/")
	if name == "" || name == "." || !dataExts[strings.ToLower(path.Ext(name))] {
		return "", fmt.Errorf("%w: data files must be .csv, .tsv, .txt or .json", ErrInvalidPath)
	}
	file, err := w.resolve(dataDir, name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	if err := writeAtomic(file, data); err != nil {
		return "", err
	}
	return dataDir + "/" + name, nil
}

// PreviewData parses a data file with a format and returns at most limit
// rows plus the total row count.
func (w *Workspace) PreviewData(file string, f model.DataFormat, limit int) (*runner.Table, int, error) {
	p, err := w.resolve(filepath.ToSlash(filepath.Clean(file)))
	if err != nil {
		return nil, 0, fmt.Errorf("data file %q must be inside the workspace: %w", file, err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, fmt.Errorf("data file %q: %w", file, ErrNotFound)
		}
		return nil, 0, err
	}
	t, err := runner.ParseData(raw, filepath.Ext(p), f)
	if err != nil {
		return nil, 0, err
	}
	total := len(t.Rows)
	if limit > 0 && len(t.Rows) > limit {
		t.Rows = t.Rows[:limit]
	}
	return t, total, nil
}
