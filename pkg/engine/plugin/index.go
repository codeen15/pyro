package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Index is a plugin catalogue: a JSON file listing installable archives.
//
//	{"plugins": [{"name": "tcp", "version": "0.1.0", "url": "tcp-0.1.0.pyro-plugin",
//	              "sha256": "…", "title": "Raw TCP", "kinds": ["protocol"]}]}
//
// Archive URLs may be relative to the index URL.
type Index struct {
	Plugins []IndexEntry `json:"plugins"`
	base    *url.URL
}

type IndexEntry struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Kinds       []string `json:"kinds,omitempty"`
	URL         string   `json:"url"`
	SHA256      string   `json:"sha256"`
}

const maxArchive = 256 << 20

// FetchIndex downloads and parses an index.
func FetchIndex(ctx context.Context, indexURL string) (*Index, error) {
	base, err := url.Parse(indexURL)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") {
		return nil, fmt.Errorf("plugin index URL %q must be http(s)", indexURL)
	}
	body, err := get(ctx, base.String(), 8<<20)
	if err != nil {
		return nil, fmt.Errorf("fetching plugin index: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("plugin index is not valid JSON: %w", err)
	}
	idx.base = base
	return &idx, nil
}

// Find returns the newest entry for name, or the exact version when given.
func (i *Index) Find(name, version string) (IndexEntry, bool) {
	var hits []IndexEntry
	for _, e := range i.Plugins {
		if e.Name == name && (version == "" || e.Version == version) {
			hits = append(hits, e)
		}
	}
	if len(hits) == 0 {
		return IndexEntry{}, false
	}
	sort.Slice(hits, func(a, b int) bool { return versionLess(hits[b].Version, hits[a].Version) })
	return hits[0], true
}

// Search matches name, title and description.
func (i *Index) Search(q string) []IndexEntry {
	q = strings.ToLower(strings.TrimSpace(q))
	latest := map[string]IndexEntry{}
	for _, e := range i.Plugins {
		if q != "" && !strings.Contains(strings.ToLower(e.Name+" "+e.Title+" "+e.Description), q) {
			continue
		}
		if cur, ok := latest[e.Name]; !ok || versionLess(cur.Version, e.Version) {
			latest[e.Name] = e
		}
	}
	out := make([]IndexEntry, 0, len(latest))
	for _, e := range latest {
		out = append(out, e)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// Download fetches an entry's archive and verifies its sha256.
func (i *Index) Download(ctx context.Context, e IndexEntry) ([]byte, error) {
	if len(e.SHA256) != 64 {
		return nil, fmt.Errorf("index entry %s@%s has no valid sha256; refusing to install", e.Name, e.Version)
	}
	ref, err := url.Parse(e.URL)
	if err != nil {
		return nil, fmt.Errorf("index entry %s: bad url: %w", e.Name, err)
	}
	u := i.base.ResolveReference(ref)
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("index entry %s: archive URL must be http(s)", e.Name)
	}
	body, err := get(ctx, u.String(), maxArchive)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", e.Name, err)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, e.SHA256) {
		return nil, fmt.Errorf("checksum mismatch for %s@%s: got %s, the index says %s", e.Name, e.Version, got, e.SHA256)
	}
	return body, nil
}

func get(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", u, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d MB", u, limit>>20)
	}
	return b, nil
}

// versionLess compares dotted numeric versions ("0.10.0" > "0.9.1").
func versionLess(a, b string) bool {
	pa, pb := strings.Split(strings.SplitN(a, "-", 2)[0], "."), strings.Split(strings.SplitN(b, "-", 2)[0], ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			fmt.Sscanf(pa[i], "%d", &x)
		}
		if i < len(pb) {
			fmt.Sscanf(pb[i], "%d", &y)
		}
		if x != y {
			return x < y
		}
	}
	return a < b
}

// Checksum returns the hex sha256 of data, for publishing index entries.
func Checksum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}
