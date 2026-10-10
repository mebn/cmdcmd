package main

import (
	_ "embed"
	"strings"
)

// version.txt holds "dev" in the repository; the release workflow replaces
// it with the release tag before building.
//
//go:embed version.txt
var versionFile string

var version = strings.TrimSpace(versionFile)
