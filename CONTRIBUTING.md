# Contributing to Nexus

Thanks for your interest in contributing! This document describes the workflow
and conventions for the repository.

## Getting Started

1. Install Go `1.22+`.
2. Fork and clone the repository.
3. Build and test:

   ```bash
   go build ./...
   go test ./...
   go vet ./...
   ```

4. Copy `configs/default.yaml` to `configs/local.yaml` and set your model API
   key via environment variables (never commit real keys).

## Development Workflow

1. Open an issue describing the bug or feature before starting large changes.
2. Create a branch off `master` with a short, descriptive name.
3. Make focused commits; reference the issue number in the commit message.
4. Keep the change scoped and reviewable.

## Code Conventions

- Format with `gofmt` (the CI fails on unformatted code).
- Prefer table-driven tests and add tests for new behavior, especially for
  security-sensitive paths (permission, sandbox, tool execution).
- Keep the `core` package decoupled: it consumes interfaces rather than
  importing concrete subsystems.
- Document exported identifiers.
- Avoid importing `internal/tool` from `internal/core`; use the existing
  `ToolRegistry` / `PermPipeline` interfaces instead.

## Testing

- Run `go test ./...` before pushing.
- Run `go test -race ./...` when touching concurrency (lanes, team runtime,
  rate limiter, executor).
- Run `go vet ./...` and `staticcheck ./...` (if installed).

## Security

Security-sensitive changes must not weaken the permission pipeline, the path
sandbox, or the gateway authentication defaults. See [SECURITY.md](SECURITY.md)
for the security model and reporting process.

## Commit Messages

Use a conventional-commit style prefix:

- `feat:` for new features
- `fix:` for bug fixes
- `security:` for security hardening
- `docs:` for documentation
- `test:` for tests
- `chore:` for tooling and maintenance

## Pull Requests

Fill in the pull request template, keep the diff focused, and make sure CI is
green before requesting review.
