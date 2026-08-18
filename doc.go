// Package netchaos provides deterministic, in-process simulated net.Conn
// and net.Listener implementations for Go tests. A Network, created with
// NewNetwork inside a testing/synctest bubble, lets peers Dial and Listen
// against each other with a seeded, reproducible RNG stream (WithSeed).
//
// Fault injection — latency (WithLatency), packet loss (WithPacketLoss),
// and network partition (WithPartition, Network.Partition/Heal) — draws
// from that same seeded stream, so a fixed seed reproduces an identical
// fault sequence across runs and machines.
package netchaos
