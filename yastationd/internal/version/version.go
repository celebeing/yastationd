// Package version holds the build's version, burned in by the Makefile from git
// (`git describe --tags --match 'v*' --dirty`: v0.3.0 on a release tag, v0.3.0-5-gabc1234 after
// it, a -dirty suffix for an uncommitted tree). A plain `go build` reports "dev".
package version

// Version is set at link time (-ldflags -X).
var Version = "dev"
