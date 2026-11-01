package httpdriver

import (
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Cookie is a stored cookie, as listed and edited in the UI.
type Cookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain"`
	Path     string    `json:"path"`
	HostOnly bool      `json:"hostOnly,omitempty"` // sent only to Domain itself, not subdomains
	Secure   bool      `json:"secure,omitempty"`
	HTTPOnly bool      `json:"httpOnly,omitempty"`
	SameSite string    `json:"sameSite,omitempty"`
	Expires  time.Time `json:"expires,omitzero"` // zero: a session cookie
	Created  time.Time `json:"created"`
	seq      uint64    // insertion order, to break ties in Created
}

// Jar is an RFC 6265 cookie jar that can also list and edit its cookies
// (net/http/cookiejar can't). Like cookiejar without a public-suffix list,
// it doesn't stop a site from setting cookies for a whole TLD; it only ever
// talks to hosts you send requests to.
type Jar struct {
	mu      sync.Mutex
	cookies map[string]*Cookie // by domain\x00path\x00name
	now     func() time.Time
	seq     uint64
	// OnChange, if set, is called (outside the lock) after cookies are
	// added, changed or removed, e.g. to save them.
	OnChange func()
}

func (j *Jar) changed() {
	if f := j.OnChange; f != nil {
		f()
	}
}

// Load replaces the jar's cookies with saved ones (expired ones skipped),
// keeping their creation order.
func (j *Jar) Load(cookies []Cookie) {
	sort.SliceStable(cookies, func(a, b int) bool { return cookies[a].Created.Before(cookies[b].Created) })
	now := j.now()
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cookies = map[string]*Cookie{}
	for _, c := range cookies {
		if !c.Expires.IsZero() && !c.Expires.After(now) {
			continue
		}
		c := c
		j.seq++
		c.seq = j.seq
		j.cookies[ckey(c.Domain, c.Path, c.Name)] = &c
	}
}

func NewJar() *Jar { return &Jar{cookies: map[string]*Cookie{}, now: time.Now} }

func ckey(domain, path, name string) string { return domain + "\x00" + path + "\x00" + name }

func canonicalHost(u *url.URL) string {
	return strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
}

func isIP(host string) bool { return net.ParseIP(host) != nil }

// domainMatch is RFC 6265 §5.1.3.
func domainMatch(host, domain string) bool {
	if host == domain {
		return true
	}
	return !isIP(host) && strings.HasSuffix(host, "."+domain)
}

// defaultPath is RFC 6265 §5.1.4.
func defaultPath(p string) string {
	if p == "" || p[0] != '/' {
		return "/"
	}
	i := strings.LastIndexByte(p, '/')
	if i == 0 {
		return "/"
	}
	return p[:i]
}

// pathMatch is RFC 6265 §5.1.4.
func pathMatch(reqPath, cookiePath string) bool {
	if reqPath == "" {
		reqPath = "/"
	}
	if reqPath == cookiePath {
		return true
	}
	if strings.HasPrefix(reqPath, cookiePath) {
		return strings.HasSuffix(cookiePath, "/") || reqPath[len(cookiePath)] == '/'
	}
	return false
}

// SetCookies implements http.CookieJar.
func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	host := canonicalHost(u)
	if host == "" || len(cookies) == 0 {
		return
	}
	defer j.changed()
	now := j.now()
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, hc := range cookies {
		c := &Cookie{Name: hc.Name, Value: hc.Value, Secure: hc.Secure, HTTPOnly: hc.HttpOnly, Created: now}
		switch hc.SameSite {
		case http.SameSiteLaxMode:
			c.SameSite = "Lax"
		case http.SameSiteStrictMode:
			c.SameSite = "Strict"
		case http.SameSiteNoneMode:
			c.SameSite = "None"
		}
		if d := strings.ToLower(strings.TrimPrefix(hc.Domain, ".")); d != "" {
			if !domainMatch(host, d) || (isIP(host) && d != host) {
				continue // a site may only set cookies for itself or a parent domain
			}
			c.Domain = d
		} else {
			c.Domain, c.HostOnly = host, true
		}
		c.Path = hc.Path
		if c.Path == "" || c.Path[0] != '/' {
			c.Path = defaultPath(u.Path)
		}
		expired := false
		switch {
		case hc.MaxAge < 0:
			expired = true
		case hc.MaxAge > 0:
			c.Expires = now.Add(time.Duration(hc.MaxAge) * time.Second)
		case !hc.Expires.IsZero():
			c.Expires = hc.Expires
			expired = !c.Expires.After(now)
		}
		k := ckey(c.Domain, c.Path, c.Name)
		if expired {
			delete(j.cookies, k)
			continue
		}
		j.seq++
		c.seq = j.seq
		if old := j.cookies[k]; old != nil {
			c.Created, c.seq = old.Created, old.seq
		}
		j.cookies[k] = c
	}
}

// Cookies implements http.CookieJar.
func (j *Jar) Cookies(u *url.URL) []*http.Cookie {
	host := canonicalHost(u)
	now := j.now()
	j.mu.Lock()
	defer j.mu.Unlock()
	var match []*Cookie
	for k, c := range j.cookies {
		if !c.Expires.IsZero() && !c.Expires.After(now) {
			delete(j.cookies, k)
			continue
		}
		if c.HostOnly && host != c.Domain || !c.HostOnly && !domainMatch(host, c.Domain) {
			continue
		}
		if !pathMatch(u.Path, c.Path) || c.Secure && u.Scheme != "https" && u.Scheme != "wss" {
			continue
		}
		match = append(match, c)
	}
	// Longer paths first, then older cookies (RFC 6265 §5.4).
	sort.Slice(match, func(a, b int) bool {
		if len(match[a].Path) != len(match[b].Path) {
			return len(match[a].Path) > len(match[b].Path)
		}
		if !match[a].Created.Equal(match[b].Created) {
			return match[a].Created.Before(match[b].Created)
		}
		return match[a].seq < match[b].seq
	})
	out := make([]*http.Cookie, len(match))
	for i, c := range match {
		out[i] = &http.Cookie{Name: c.Name, Value: c.Value}
	}
	return out
}

// List returns every live cookie, by domain, path and name.
func (j *Jar) List() []Cookie {
	now := j.now()
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Cookie, 0, len(j.cookies))
	for k, c := range j.cookies {
		if !c.Expires.IsZero() && !c.Expires.After(now) {
			delete(j.cookies, k)
			continue
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Domain != out[b].Domain {
			return out[a].Domain < out[b].Domain
		}
		if out[a].Path != out[b].Path {
			return out[a].Path < out[b].Path
		}
		return out[a].Name < out[b].Name
	})
	return out
}

// Put adds or replaces a cookie (from the UI). Domain and Name are required;
// Path defaults to "/".
func (j *Jar) Put(c Cookie) {
	defer j.changed()
	c.Domain = strings.ToLower(strings.TrimPrefix(c.Domain, "."))
	if c.Path == "" {
		c.Path = "/"
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	k := ckey(c.Domain, c.Path, c.Name)
	c.Created = j.now()
	j.seq++
	c.seq = j.seq
	if old := j.cookies[k]; old != nil {
		c.Created, c.seq = old.Created, old.seq
	}
	j.cookies[k] = &c
}

// Delete removes cookies: one (domain, path, name), every cookie of a
// domain (name and path empty), or all (everything empty).
func (j *Jar) Delete(domain, path, name string) int {
	defer j.changed()
	domain = strings.ToLower(strings.TrimPrefix(domain, "."))
	j.mu.Lock()
	defer j.mu.Unlock()
	n := 0
	for k, c := range j.cookies {
		if (domain == "" || c.Domain == domain) && (name == "" || c.Name == name) && (path == "" || c.Path == path) {
			delete(j.cookies, k)
			n++
		}
	}
	return n
}
