//go:build !plan9

package netchaos

import "syscall"

// errConnReset is the error a reset connection's Read and Write wrap
// (Network.Reset, M7-7): syscall.ECONNRESET, so callers can match it with
// errors.Is the same way they would a real TCP reset. plan9 has no
// ECONNRESET in its syscall package; see reset_errno_plan9.go (#113).
var errConnReset error = syscall.ECONNRESET
