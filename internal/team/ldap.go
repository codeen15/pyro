package team

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// LDAPConfig signs people in against LDAP or Active Directory: Pyro binds
// with a service account, finds the user, then binds as them with the
// password they typed.
type LDAPConfig struct {
	// URL is ldap://host:389 or ldaps://host:636.
	URL string `yaml:"url"`
	// StartTLS upgrades an ldap:// connection to TLS.
	StartTLS bool `yaml:"startTLS"`
	// InsecureSkipVerify skips certificate checks (testing only).
	InsecureSkipVerify bool `yaml:"insecureSkipVerify"`
	// BindDN and BindPassword are the service account used to search
	// (empty: anonymous). The password defaults to $PYRO_LDAP_BIND_PASSWORD.
	BindDN       string `yaml:"bindDN"`
	BindPassword string `yaml:"bindPassword"`
	// BaseDN is where users are searched, e.g. ou=people,dc=example,dc=com.
	BaseDN string `yaml:"baseDN"`
	// UserFilter finds the user; {username} is replaced (escaped) by what
	// they typed. Default matches uid, mail or sAMAccountName.
	UserFilter string `yaml:"userFilter"`
	// EmailAttribute and NameAttribute default to mail and displayName (cn).
	EmailAttribute string `yaml:"emailAttribute"`
	NameAttribute  string `yaml:"nameAttribute"`
	// AdminGroup is a group DN; members are server administrators (checked
	// with memberOf at each sign-in). Empty: admin isn't managed by LDAP.
	AdminGroup string `yaml:"adminGroup"`
}

const defaultUserFilter = "(|(uid={username})(mail={username})(sAMAccountName={username}))"

// DirectoryUser is a person the directory vouched for.
type DirectoryUser struct {
	DN, Email, Name string
	// Admin is nil when the directory doesn't manage administration.
	Admin *bool
}

// Directory checks a username and password (LDAP, or a fake in tests).
type Directory interface {
	Authenticate(ctx context.Context, username, password string) (*DirectoryUser, error)
}

// errDirectoryLogin is a wrong username or password; anything else is the
// directory being unreachable or misconfigured.
var errDirectoryLogin = errors.New("wrong username or password")

// NewLDAP returns a Directory for c, or nil when LDAP isn't configured.
func NewLDAP(c LDAPConfig) (Directory, error) {
	if c.URL == "" {
		return nil, nil
	}
	if c.BaseDN == "" {
		return nil, errors.New("ldap: baseDN is required")
	}
	if c.UserFilter == "" {
		c.UserFilter = defaultUserFilter
	}
	if !strings.Contains(c.UserFilter, "{username}") {
		return nil, errors.New("ldap: userFilter must contain {username}")
	}
	if c.EmailAttribute == "" {
		c.EmailAttribute = "mail"
	}
	if c.NameAttribute == "" {
		c.NameAttribute = "displayName"
	}
	if c.BindPassword == "" {
		c.BindPassword = os.Getenv("PYRO_LDAP_BIND_PASSWORD")
	}
	return &ldapDirectory{c}, nil
}

type ldapDirectory struct{ c LDAPConfig }

func (d *ldapDirectory) dial() (*ldap.Conn, error) {
	host := d.c.URL
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	tc := &tls.Config{ServerName: host, InsecureSkipVerify: d.c.InsecureSkipVerify, MinVersion: tls.VersionTLS12}
	conn, err := ldap.DialURL(d.c.URL, ldap.DialWithTLSConfig(tc))
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(10 * time.Second)
	if d.c.StartTLS {
		if err := conn.StartTLS(tc); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

func (d *ldapDirectory) Authenticate(ctx context.Context, username, password string) (*DirectoryUser, error) {
	username = strings.TrimSpace(username)
	// An empty password would be an "unauthenticated bind", which servers
	// accept without checking anything.
	if username == "" || password == "" {
		return nil, errDirectoryLogin
	}
	conn, err := d.dial()
	if err != nil {
		return nil, fmt.Errorf("ldap: connect: %w", err)
	}
	defer conn.Close()
	if d.c.BindDN != "" {
		if err := conn.Bind(d.c.BindDN, d.c.BindPassword); err != nil {
			return nil, fmt.Errorf("ldap: service account bind: %w", err)
		}
	}
	filter := strings.ReplaceAll(d.c.UserFilter, "{username}", ldap.EscapeFilter(username))
	attrs := []string{d.c.EmailAttribute, d.c.NameAttribute, "cn", "memberOf"}
	res, err := conn.Search(ldap.NewSearchRequest(d.c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 10, false, filter, attrs, nil))
	switch {
	case err == nil, ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded):
	case ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject):
		// Some servers answer a search with no results this way; it also
		// means a wrong baseDN, so say so in the log.
		slog.Warn("LDAP search found nothing (No Such Object); check baseDN if nobody can sign in", "baseDN", d.c.BaseDN)
		return nil, errDirectoryLogin
	default:
		return nil, fmt.Errorf("ldap: search: %w", err)
	}
	if res == nil || len(res.Entries) != 1 {
		return nil, errDirectoryLogin // nobody, or ambiguous
	}
	e := res.Entries[0]
	if err := conn.Bind(e.DN, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, errDirectoryLogin
		}
		return nil, fmt.Errorf("ldap: user bind: %w", err)
	}
	u := &DirectoryUser{DN: e.DN, Email: e.GetAttributeValue(d.c.EmailAttribute), Name: e.GetAttributeValue(d.c.NameAttribute)}
	if u.Name == "" {
		u.Name = e.GetAttributeValue("cn")
	}
	if d.c.AdminGroup != "" {
		admin := false
		for _, g := range e.GetAttributeValues("memberOf") {
			admin = admin || strings.EqualFold(strings.ReplaceAll(g, " ", ""), strings.ReplaceAll(d.c.AdminGroup, " ", ""))
		}
		u.Admin = &admin
	}
	return u, nil
}
