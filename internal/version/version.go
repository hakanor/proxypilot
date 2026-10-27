// Package version holds build metadata, set with -ldflags:
//
//	-X github.com/hakanor/proxypilot/internal/version.Version=v0.1.0
//	-X github.com/hakanor/proxypilot/internal/version.Commit=abc123
//	-X github.com/hakanor/proxypilot/internal/version.Date=2026-10-26T12:00:00Z
package version

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
