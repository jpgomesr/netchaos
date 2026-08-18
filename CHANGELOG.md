# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
once the first release is tagged.

## [Unreleased]

- Core simulated transport: `Network`, `Dial`/`DialContext`, `Listen`, deadlines, and address/peer naming (M1).
- Deterministic fault injection: seeded per-connection RNG streams, latency (`WithLatency`), packet loss (`WithPacketLoss`), and network partition (`WithPartition`, `Network.Partition`/`Heal`, `WithPeerName`), composed into a single evaluation order with option validation (M2).
- Project scaffolding: CI, linting, issue/PR templates, contributing/security/conduct policies.
- Design documentation ([docs/](docs/README.md)) covering vision, architecture, API design, and scope.
- Task breakdown ([docs/tasks/](docs/tasks/README.md)) sequencing the v1 scope into milestones.

No release has been tagged yet.
