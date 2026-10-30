# ADR 0004: Mobile support — responsive web app, not a native app

**Date:** 2026-08-17
**Status:** Accepted

## Context
Mobile usability is wanted, but not an app-store presence. Two real options:
a native/cross-platform app (Flutter) with its own codebase, build pipeline,
and store distribution; or a responsive web frontend, optionally installable
as a PWA.

ADR 0001 already committed to server-rendered templates + htmx specifically
to avoid a separate frontend build pipeline. Flutter would reintroduce
exactly that: a second codebase (Dart), a second build/release process, app
store or sideloading logistics — none of which is needed just to make pages
usable on a phone.

## Decision
Responsive web frontend, mobile-first CSS from Phase 1 onward — not a
separate design phase, just a constraint on how templates are written.
Add a PWA manifest + service worker (installable "add to home screen",
cached static assets) so it feels app-like without being one.

No native app, no Flutter, no app store distribution.

## Consequences
+ One codebase, one deploy artifact — consistent with ADR 0001's simplicity goal
+ No app store review process, signing, or update-rollout logistics
+ Works immediately on any device with a browser, including desktop
- Offline capability is limited to cached UI shell/assets, not offline data
  entry with sync — the app still needs a live connection to the Go backend
  to do anything useful. Acceptable: this is a bookkeeping tool for a small
  business's own server, not a field app needing offline-first data capture.
- Camera access for receipt photos (feeding the OCR pipeline from ADR 0002)
  works via the browser's file input / `capture` attribute on mobile — covers
  the need without a native camera API.

## Alternatives considered
- **Flutter app:** rejected — duplicates the frontend stack, adds a build/
  release pipeline and store distribution that contradicts the project's
  core "as simple as possible" constraint, for a benefit (offline-first,
  native camera APIs) that isn't actually needed here.
