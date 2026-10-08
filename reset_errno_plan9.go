//go:build plan9

package netchaos

import "errors"

// errConnReset is the error a reset connection's Read and Write wrap
// (Network.Reset, M7-7). plan9's syscall package has no ECONNRESET, so this
// is a plain error carrying the same message the other platforms' errno
// prints; see reset_errno.go (#113).
var errConnReset = errors.New("connection reset by peer")
