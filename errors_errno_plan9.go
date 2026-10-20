//go:build plan9

package netchaos

// plan9 has neither standard errno; the exported sentinels retain their own
// errors.Is identity and messages without matching a platform errno.
var (
	errConnRefused error
	errAddrInUse   error
)
