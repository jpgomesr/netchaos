//go:build plan9

package netchaos

import "errors"

// plan9 has no syscall.EPIPE; keep the familiar message as a plain error.
var errBrokenPipe = errors.New("broken pipe")
