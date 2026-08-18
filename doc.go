// Package netchaos provides deterministic, in-process simulated net.Conn
// and net.Listener implementations for Go tests. A Network, created with
// NewNetwork inside a testing/synctest bubble, lets peers Dial and Listen
// against each other with a seeded, reproducible RNG stream (WithSeed).
//
// Fault injection — latency, packet loss, and network partition — is
// planned for a future milestone and not yet implemented.
package netchaos
