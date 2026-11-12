package main

import (
	"github.com/monorithm/mai/cli/v3/internal/connect"
	"github.com/monorithm/mai/cli/v3/internal/uninstall"
)

// The uninstall package removes app entries through the connect package. The link is here,
// because the two packages do not import each other.
func init() {
	uninstall.RemoveAppEntry = connect.RemoveEntry
	uninstall.RemoveCreatedAppFile = connect.RemoveCreatedFile
}
