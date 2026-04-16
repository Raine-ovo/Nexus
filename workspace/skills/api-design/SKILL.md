---
name: api-design
description: REST and RPC API design principles covering naming, versioning, error handling, and pagination.
invocation: Use when designing new APIs, reviewing API contracts, or planning service interfaces.
---

# API Design Skill

## Resource Naming
- Use plural nouns for collections: `/users`, `/orders`, `/products`.
- Nest sub-resources for clear ownership: `/users/{id}/addresses`.
- Avoid verbs in paths; rely on HTTP methods: `POST /orders` not `POST /createOrder`.
- Use kebab-case for multi-word path segments: `/order-items`, not `/orderItems`.

## HTTP Methods and Semantics
- GET: safe, idempotent read. Never mutate state.
- POST: create a new resource or trigger a non-idempotent action.
- PUT: full replace of a resource (idempotent).
- PATCH: partial update (idempotent when using JSON Merge Patch or JSON Patch).
- DELETE: remove a resource (idempotent — deleting a non-existent resource returns 204 or 404 consistently).

## Versioning
- Prefer URL prefix versioning (`/v1/`, `/v2/`) for simplicity and cache-friendliness.
- Breaking changes (field removal, type change, semantic shift) require a version bump.
- Non-breaking additions (new optional fields, new endpoints) can go into the current version.
- Deprecate old versions with a sunset header and migration timeline.

## Error Responses
- Use a consistent error envelope across all endpoints:
  ```json
  {
    "error": {
      "code": "INVALID_ARGUMENT",
      "message": "email format is invalid",
      "details": [...]
    }
  }
  ```
- Map errors to appropriate HTTP status codes: 400 (bad request), 401 (unauthenticated), 403 (forbidden), 404 (not found), 409 (conflict), 422 (unprocessable), 429 (rate limited), 500 (internal).
- Include a machine-readable `code` string; never rely on status code alone for error classification.
- Do not leak stack traces, internal paths, or SQL errors to clients.

## Pagination
- Use cursor-based pagination for large or frequently-mutated collections.
- Response shape: `{ "data": [...], "next_cursor": "...", "has_more": true }`.
- Offset-based (`?page=2&per_page=20`) is acceptable for small, stable datasets.
- Always cap `per_page` server-side (e.g., max 100).

## Request / Response Conventions
- Accept and return JSON with `Content-Type: application/json`.
- Use `snake_case` for JSON field names (consistent with Go JSON tags and most backend conventions).
- Timestamps in ISO 8601 / RFC 3339 format with timezone: `2024-01-15T09:30:00Z`.
- Monetary amounts as integer minor units (cents) with a `currency` field, not floating-point.

## Idempotency
- POST endpoints that create resources should accept an `Idempotency-Key` header.
- Store the key → response mapping for a reasonable TTL (e.g., 24 hours).
- Return the cached response on duplicate key submission instead of creating duplicates.

## Rate Limiting
- Return `429 Too Many Requests` with `Retry-After` header.
- Document rate limits in API docs and expose them via response headers (`X-RateLimit-Limit`, `X-RateLimit-Remaining`).
- Apply limits per API key / token, not per IP alone.

## RPC-Style Endpoints
- For actions that don't map cleanly to CRUD, use a verb sub-resource: `POST /orders/{id}/cancel`.
- Keep the request body as a structured command object, not loose query parameters.
- Return the updated resource state in the response.

## Documentation
- Every endpoint must have: summary, description, request/response schemas, example payloads, and error codes.
- Use OpenAPI 3.x as the source of truth; generate SDKs and docs from it.
- Include authentication requirements and required scopes per endpoint.
