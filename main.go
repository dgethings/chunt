package main

import (
	"os"
	"runtime/debug"

	"github.com/dgethings/chunt/cmd"
)

// version is stamped at build time via -ldflags "-X main.version=<tag>" by
// both goreleaser (releases) and the Makefile (local builds, via `svu
// current`). "dev" means it was not stamped — e.g. a bare `go build` or
// `go install`, which cannot apply -X — in which case fall back to the
// module version recorded by the Go toolchain, if any.
var version = "dev"

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func main() {
	cmd.Version = resolveVersion()
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
