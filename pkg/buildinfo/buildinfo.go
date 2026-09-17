// Package buildinfo carries the version stamped into the binary at build time.
package buildinfo

// Version and Commit are set through -ldflags; "dev" and "unknown" mean a
// plain go build.
var (
	Version = "dev"
	Commit  = "unknown"
)
