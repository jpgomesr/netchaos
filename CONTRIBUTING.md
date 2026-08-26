# Contributing to netchaos

## Project status

netchaos v1 is implemented, tested, and documented. See [`docs/07-contributing.md`](docs/07-contributing.md) for what's most useful to contribute *right now* — it's kept more current than the mechanics below.

## Development setup

```
go build ./...
go vet ./...
go test -race ./...
gofmt -l .   # should print nothing
```

CI runs the same checks (`.github/workflows/ci.yml`) plus `golangci-lint` (`.github/workflows/golangci-lint.yml`) on every push and pull request.

## Commit messages

This repo follows [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, `chore:`, etc.), matching the existing commit history.

## Pull requests

- Keep PRs focused — one logical change per PR.
- If the change touches API shape or scope, link the relevant doc under `docs/` and expect discussion before merge, since the core interfaces are still being designed (see [docs/07-contributing.md](docs/07-contributing.md)).
- Fill out the PR template checklist.

## License

netchaos is licensed under [MIT](LICENSE). Contributions are accepted under the same license.
