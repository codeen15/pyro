// Package har imports HTTP Archive (HAR 1.2) recordings from browser dev
// tools or proxies. Requests keep their recorded order, grouped into one
// folder per host; static assets are skipped.
package har

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/model"
)

type harFile struct {
	Log struct {
		Creator struct {
			Name string `json:"name"`
		} `json:"creator"`
		Pages []struct {
			Title string `json:"title"`
		} `json:"pages"`
		Entries []entry `json:"entries"`
	} `json:"log"`
}

type entry struct {
	Request struct {
		Method      string `json:"method"`
		URL         string `json:"url"`
		Headers     []nv   `json:"headers"`
		QueryString []nv   `json:"queryString"`
		PostData    *struct {
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
			Params   []struct {
				Name     string `json:"name"`
				Value    string `json:"value"`
				FileName string `json:"fileName"`
			} `json:"params"`
		} `json:"postData"`
	} `json:"request"`
	ResourceType string `json:"_resourceType"` // Chrome
}

type nv struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Looks reports whether data is a HAR file.
func Looks(data []byte) bool {
	var probe struct {
		Log *struct {
			Entries json.RawMessage `json:"entries"`
		} `json:"log"`
	}
	return json.Unmarshal(data, &probe) == nil && probe.Log != nil && probe.Log.Entries != nil
}

// skipTypes are Chrome resource types that aren't API calls.
var skipTypes = map[string]bool{"image": true, "font": true, "stylesheet": true, "script": true, "media": true, "manifest": true, "texttrack": true, "ping": true, "preflight": true}

// dropHeaders are set by the client per request, or are HTTP/2 pseudo-headers.
var dropHeaders = map[string]bool{"host": true, "content-length": true, "connection": true, "accept-encoding": true, "keep-alive": true, "transfer-encoding": true}

// Parse converts a HAR file into a collection named name (default: the
// first page title, else "HAR import").
func Parse(name string, data []byte) (*importer.Bundle, error) {
	var h harFile
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("not a valid HAR file: %w", err)
	}
	if len(h.Log.Entries) == 0 {
		return nil, errors.New("the HAR file has no requests")
	}
	if name == "" && len(h.Log.Pages) > 0 && h.Log.Pages[0].Title != "" {
		name = h.Log.Pages[0].Title
		if u, err := url.Parse(name); err == nil && u.Host != "" {
			name = u.Host
		}
	}
	if name == "" {
		name = "HAR import"
	}
	b := &importer.Bundle{
		Collection: &model.Collection{Name: name},
		Slug:       importer.Slugify(name),
		Folders:    map[string]*model.Collection{},
		Report:     importer.Report{Format: "har", Collection: name, Warnings: []importer.Warning{}},
	}
	type folder struct {
		taken map[string]bool
		order []string
	}
	folders := map[string]*folder{}
	var hostOrder []string
	skipped, cookies := 0, false
	for _, e := range h.Log.Entries {
		if skipTypes[e.ResourceType] || e.Request.Method == "OPTIONS" {
			skipped++
			continue
		}
		u, err := url.Parse(e.Request.URL)
		if err != nil || u.Host == "" {
			b.Report.Warn("", "har.url", fmt.Sprintf("skipped a request with URL %q", e.Request.URL))
			continue
		}
		hostSlug := importer.Slugify(u.Host)
		f := folders[hostSlug]
		if f == nil {
			f = &folder{taken: map[string]bool{}}
			folders[hostSlug] = f
			hostOrder = append(hostOrder, hostSlug)
			b.Folders[hostSlug] = &model.Collection{Name: u.Host}
			b.Report.Folders++
		}
		r := &model.Request{Kind: model.KindHTTP, Method: strings.ToUpper(e.Request.Method)}
		if u.Scheme == "ws" || u.Scheme == "wss" || e.ResourceType == "websocket" {
			r.Kind, r.Method = model.KindWebSocket, ""
		}
		p := u.Path
		if p == "" {
			p = "/"
		}
		r.Name = strings.TrimSpace(r.Method + " " + p)
		slug := importer.Unique(importer.Slugify(r.Name), f.taken)
		f.order = append(f.order, slug)
		path := hostSlug + "/" + slug

		// Query parameters move into the list (from queryString, which is
		// decoded, else from the URL).
		if len(e.Request.QueryString) > 0 {
			for _, q := range e.Request.QueryString {
				r.Query = append(r.Query, model.KeyValue{Key: q.Name, Value: q.Value})
			}
		} else {
			for _, part := range strings.Split(u.RawQuery, "&") {
				if part == "" {
					continue
				}
				k, v, _ := strings.Cut(part, "=")
				ku, _ := url.QueryUnescape(k)
				vu, _ := url.QueryUnescape(v)
				r.Query = append(r.Query, model.KeyValue{Key: ku, Value: vu})
			}
		}
		u.RawQuery, u.Fragment = "", ""
		r.URL = u.String()

		ctype := ""
		for _, hd := range e.Request.Headers {
			k := strings.ToLower(hd.Name)
			if strings.HasPrefix(k, ":") || dropHeaders[k] || (r.Kind == model.KindWebSocket && strings.HasPrefix(k, "sec-websocket")) {
				continue
			}
			if k == "content-type" {
				ctype = strings.ToLower(hd.Value)
			}
			if k == "cookie" {
				cookies = true
			}
			r.Headers = append(r.Headers, model.KeyValue{Key: hd.Name, Value: hd.Value})
		}
		if pd := e.Request.PostData; pd != nil && r.Kind == model.KindHTTP {
			mt := strings.ToLower(pd.MimeType)
			if mt == "" {
				mt = ctype
			}
			switch {
			case strings.Contains(mt, "multipart/form-data"):
				var fields []model.KeyValue
				for _, prm := range pd.Params {
					kv := model.KeyValue{Key: prm.Name, Value: prm.Value}
					if prm.FileName != "" {
						kv.File, kv.Value = true, prm.FileName
						b.Report.Warn(path, "body.file", fmt.Sprintf("form field %q uploaded %s; its contents aren't in the HAR, pick the file before sending", prm.Name, prm.FileName))
					}
					fields = append(fields, kv)
				}
				r.Body = &model.Body{Type: model.BodyMultipart, Fields: fields}
				removeHeader(r, "Content-Type")
			case strings.Contains(mt, "x-www-form-urlencoded"):
				var fields []model.KeyValue
				if len(pd.Params) > 0 {
					for _, prm := range pd.Params {
						k, _ := url.QueryUnescape(prm.Name)
						v, _ := url.QueryUnescape(prm.Value)
						fields = append(fields, model.KeyValue{Key: k, Value: v})
					}
				} else if vals, err := url.ParseQuery(pd.Text); err == nil {
					for _, part := range strings.Split(pd.Text, "&") {
						k, _, _ := strings.Cut(part, "=")
						ku, _ := url.QueryUnescape(k)
						fields = append(fields, model.KeyValue{Key: ku, Value: vals.Get(ku)})
					}
				}
				r.Body = &model.Body{Type: model.BodyForm, Fields: fields}
				removeHeader(r, "Content-Type")
			case pd.Text == "":
			case strings.Contains(mt, "json"):
				r.Body = &model.Body{Type: model.BodyJSON, Content: pd.Text}
			case strings.Contains(mt, "xml"):
				r.Body = &model.Body{Type: model.BodyXML, Content: pd.Text}
			default:
				r.Body = &model.Body{Type: model.BodyText, Content: pd.Text}
			}
		}
		b.Requests = append(b.Requests, importer.Request{Path: path, Request: r})
		b.Report.Requests++
	}
	if len(b.Requests) == 0 {
		return nil, fmt.Errorf("the HAR file has no API requests (%d static assets skipped)", skipped)
	}
	for slug, f := range folders {
		if len(f.order) > 1 {
			b.Folders[slug].Order = f.order
		}
	}
	if len(hostOrder) > 1 {
		b.Collection.Order = hostOrder
	}
	if skipped > 0 {
		b.Report.Warn("", "har.skipped", fmt.Sprintf("%d static assets and preflight requests were skipped", skipped))
	}
	if cookies {
		b.Report.Warn("", "har.cookies", "recorded Cookie headers were kept; they may hold session secrets and will expire")
	}
	b.Report.Slug = b.Slug
	return b, nil
}

func removeHeader(r *model.Request, k string) {
	out := r.Headers[:0]
	for _, h := range r.Headers {
		if !strings.EqualFold(h.Key, k) {
			out = append(out, h)
		}
	}
	r.Headers = out
}
