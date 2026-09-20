# Security Policy

## Reporting a Vulnerability

Please **do not** open a public issue for security vulnerabilities.

Instead, report them privately to the maintainers. We will acknowledge your
report within 3 business days and aim to provide a fix or a coordinated
disclosure timeline within 30 days.

When reporting, please include:

- A description of the vulnerability and its impact.
- Steps to reproduce, including the configuration used (remember that
  `configs/default.yaml` contains no secrets — never include real API keys).
- The affected version / commit.
- Any suggested mitigation.

## Supported Versions

| Version | Supported |
| ------- | --------- |
| latest `master` | :white_check_mark: |

## Security Model

Nexus runs LLM-driven agents that can execute tools (shell, file writes, HTTP
requests). Please keep these boundaries in mind:

1. **Authentication.** The gateway refuses to start with empty auth on a
   non-loopback bind address. Always configure `gateway.auth.api_keys` or
   `gateway.auth.jwt_secret` before exposing it beyond `127.0.0.1`.
2. **Tool permissions.** Every tool execution path (agents, delegates,
   teammates, MCP, cron) is routed through the permission pipeline. Review
   `permission.mode` and the allow/deny rules before enabling `full_auto`.
3. **Sandbox.** The path sandbox and `utils.SafePath` resolve symlinks to
   prevent symlink-based escapes. For hostile workloads, additionally run the
   process under a container or an unprivileged user.
4. **Secrets.** Use environment variables (`${NEXUS_API_KEY}`) rather than
   committing keys to `configs/local.yaml` (which is git-ignored).

## Security Best Practices

- Bind to loopback (`127.0.0.1`) for local development.
- Put Nexus behind a trusted reverse proxy and configure
  `gateway.rate_limit.trusted_proxies` when X-Forwarded-For should be honored.
- Restrict debug endpoints (`/api/debug/*`, `/debug/dashboard`) to authenticated
  operators.
- Keep the Go toolchain and dependencies up to date; run `govulncheck` in CI.
