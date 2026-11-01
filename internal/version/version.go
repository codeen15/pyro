// Package version holds build metadata, overridden at link time:
//
//	go build -ldflags "-X github.com/pyrohq/pyro/internal/version.Version=v0.1.0"
package version

var (
	Version = "dev"
	Commit  = "none"
)
