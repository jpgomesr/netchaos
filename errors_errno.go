//go:build !plan9

package netchaos

import "syscall"

var (
	errConnRefused error = syscall.ECONNREFUSED
	errAddrInUse   error = syscall.EADDRINUSE
)
