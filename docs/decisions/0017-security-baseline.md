# ADR 0017: Security baseline: cross-origin protection, CSP, optional single-password login

**Date:** 2026-09-29
**Status:** Accepted

## Context
Plan 0001 deferred authentication as "multi-user complexity", and CLAUDE.md
makes multi-tenant auth a non-goal. Two real gaps remained:

- **Any web page could post to the app.** A form on another site, opened in
  the same browser, could book entries, issue invoices or change settings (CSRF).
  With no login at all this is worse, not better.
- **Anyone on the network could read and write the books.** "Deploy behind a
  reverse proxy" was the answer, but nothing enforced it or warned when it
  was missing.

## Decision

### Cross-origin protection, from the standard library
`http.CrossOriginProtection` (Go 1.25) wraps the whole app. It refuses
non-safe requests whose `Sec-Fetch-Site` says cross-site, or whose `Origin`
does not match the host. No tokens have to be threaded through every form,
because the templates stay plain HTML. Refusals answer 403.

### Security headers on every response
- `Content-Security-Policy: default-src 'self'; script-src 'self';
  style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none';
  form-action 'self'; frame-ancestors 'none'`. This is possible because ADR
  0013 removed every inline script and style attribute, and a test keeps it so.
- `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy: same-origin`, and a `Permissions-Policy` that allows only
  the camera (for receipt photos).
- Stored Belege are served with a stricter `sandbox` CSP of their own (ADR 0016).

### Optional single-password login
`BOOKKEEPER_PASSWORD` (or `BOOKKEEPER_PASSWORD_FILE` for container secrets)
turns on a login for everything except `/healthz`, `/login`, `/sw.js` and
`/static/`.

- **One shared password, no accounts.** It is one business's books.
  Accounts, roles and password resets are the multi-user complexity the
  non-goal is about. A shared password is not.
- **Stateless signed session.** The cookie is `<expiry>.<HMAC-SHA256>`,
  with the key derived from the password. A restart keeps sessions, and
  changing the password ends all of them. It is `HttpOnly`, `SameSite=Lax`,
  valid for 30 days, and `Secure` when the request came over TLS or with
  `X-Forwarded-Proto: https`.
- The password is compared in constant time. Every failed attempt holds a
  process-wide lock for a second, which throttles guessing to one try per
  second in total.
- `next` after login must be a local path, so the login page cannot redirect
  to another site.
- Unauthenticated GETs redirect to the login, and other methods get 401.

### A warning instead of a silent default
With no password and a listen address that is not loopback, the binary logs a
warning at startup. It does not refuse to start: a proxy that already
authenticates (for example basic auth in Caddy or Traefik, or an SSO forward-auth)
is a legitimate setup.

## Consequences
- The app is safe to reach on a LAN with a password set. TLS is still the
  proxy's job (open question in CLAUDE.md).
- Tests post without `Origin` or `Sec-Fetch-Site` headers, which the protection
  allows, as it does for non-browser clients. A dedicated test covers the
  refusal.
- There is no audit of *who* booked what. With one shared identity there is
  nobody to name, and the append-only ledger still records *what* happened and
  when.

## Alternatives considered
- **Hand-rolled CSRF tokens:** more code in every form, for what the stdlib now
  does from headers.
- **HTTP basic auth in the app:** browsers cannot log out of it, it has an
  ugly prompt, and the password goes with every request.
- **User accounts with bcrypt:** the non-goal. It would be revisited when a
  second person with different rights actually appears.
- **Refuse to start without a password on a non-loopback address:** this
  breaks proxy-authenticated setups, so it is a warning instead.
