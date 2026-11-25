// Package buildinfo carries the release identity stamped by -ldflags.
package buildinfo

var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)
