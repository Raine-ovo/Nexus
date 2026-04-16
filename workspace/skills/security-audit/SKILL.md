---
name: security-audit
description: Security auditing guidelines for web services covering injection, authentication, secrets, and supply chain risks.
invocation: Use when performing security reviews, auditing auth flows, or checking for vulnerabilities.
---

# Security Audit Skill

## Injection Prevention
- SQL: always use parameterized queries or prepared statements; never concatenate user input into query strings.
- Command injection: avoid `os/exec` with user-controlled arguments; if unavoidable, use allowlists and quote properly.
- Path traversal: validate and canonicalize paths with `filepath.Clean` + `filepath.Rel` against a known root.
- Template injection: use `html/template` (not `text/template`) for HTML output; escape dynamic attributes.

## Authentication & Authorization
- Passwords must be hashed with bcrypt (cost ≥ 12) or argon2id; never store plaintext or MD5/SHA hashes.
- JWTs: validate `iss`, `aud`, `exp`, and `nbf`; reject `alg: none`; use asymmetric keys in production.
- Session tokens: cryptographically random (≥ 128 bits), HttpOnly, Secure, SameSite=Lax/Strict.
- Enforce least-privilege: default-deny access control; check ownership on every mutation endpoint.

## Secrets Management
- No secrets in source: API keys, tokens, certificates, and connection strings belong in env vars or a vault.
- Scan for high-entropy strings, `password=`, `secret=`, `token=`, and base64-encoded private keys.
- Rotate credentials on exposure; automate rotation where supported.
- `.env` files must be in `.gitignore`; CI secrets use masked variables.

## Transport Security
- Enforce TLS 1.2+ for all external connections; disable SSLv3 and weak cipher suites.
- Enable HSTS with `max-age >= 31536000`; include subdomains for public-facing services.
- Validate TLS certificates in HTTP clients; never set `InsecureSkipVerify: true` in production.

## Data Protection
- PII and sensitive fields: encrypt at rest (AES-256-GCM or equivalent); mask in logs and error messages.
- Rate-limit authentication endpoints (login, password reset) to mitigate brute force.
- Implement audit logging for privileged operations: who, what, when, from where.

## Dependency and Supply Chain
- Pin dependency versions; review diffs on updates.
- Run `govulncheck` (Go) or equivalent scanners in CI.
- Avoid importing abandoned or single-maintainer packages for security-critical functionality.
- Container images: use minimal base (distroless/alpine), run as non-root, scan with Trivy or Grype.

## Common Vulnerability Patterns
- SSRF: validate and restrict outbound URLs; block internal/private IP ranges.
- CORS misconfiguration: never reflect `Origin` blindly as `Access-Control-Allow-Origin`.
- Deserialization: avoid deserializing untrusted data with `encoding/gob` or `unsafe`; prefer JSON with strict schemas.
- Timing attacks: use `subtle.ConstantTimeCompare` for secret comparison.
