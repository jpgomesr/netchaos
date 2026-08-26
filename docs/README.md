# netchaos design docs

> **Status: v1 implemented.** The core transport, all three v1 fault types (latency, packet loss, partition), and `testing/synctest` integration are built, tested, and documented. See [`CHANGELOG.md`](../CHANGELOG.md) for the release history.

This folder expands on the pitch made in the root [`README.md`](../README.md) into full design documentation, organized by topic:

| Doc | Covers |
|---|---|
| [01 — Vision](01-vision.md) | What netchaos is, what it deliberately is not, who it's for, and the philosophy behind it |
| [02 — Comparison](02-comparison.md) | Detailed comparison against Toxiproxy, gosim, Chaos Mesh/Litmus, and Antithesis |
| [03 — Architecture](03-architecture.md) | How the in-process simulated network is structured conceptually |
| [04 — API Design](04-api-design.md) | The frozen v1 Go API surface (interfaces, structs, functional options) — agreed and implemented |
| [05 — Fault Injection](05-fault-injection.md) | Deep dive on each fault type: latency, packet loss, partition |
| [06 — Scope & Roadmap](06-scope-and-roadmap.md) | v1 scope, why the boundaries exist, and what's deliberately deferred |
| [07 — Contributing](07-contributing.md) | Project status and how to contribute at this stage |
| [Task breakdown](tasks/README.md) | The v1 scope broken into executable tasks, grouped into milestones |

## Reading order

If you're new to the project, read them in numeric order: 01 gives you the "why", 02 places netchaos relative to tools you may already know, 03–05 describe the design itself, and 06–07 cover scope and process.

Once you've read those, [`tasks/`](tasks/README.md) turns the v1 scope checklist in [06](06-scope-and-roadmap.md) into sequenced, detailed tasks — read it when you want to know *what to build next*, not *what netchaos is*.
