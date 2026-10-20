//go:build !plan9

package netchaos

import "syscall"

var errBrokenPipe error = syscall.EPIPE
