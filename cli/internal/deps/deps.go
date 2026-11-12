// Package deps pins the modules that the packages of the CLI use, so that the go.mod of the
// module has one set of versions.
package deps

import (
	_ "github.com/moby/moby/api/types/container"
	_ "github.com/moby/moby/client"
	_ "github.com/zalando/go-keyring"
	_ "golang.org/x/sys/unix"
)
