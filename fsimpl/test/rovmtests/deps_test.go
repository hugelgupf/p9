//go:build tools
// +build tools

package rovmtests_test

// List u-root commands that need to be in go.mod & go.sum to be buildable as
// dependencies. This way, they aren't eliminated by `go mod tidy`.
//
// But obviously aren't actually importable, since they are main packages.
import (
	_ "github.com/u-root/u-root/cmds/core/dhclient"
)
