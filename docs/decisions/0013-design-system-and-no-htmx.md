# ADR 0013: Design system, app shell, and one small script instead of htmx

**Date:** 2026-09-29
**Status:** Accepted. Supersedes ADR 0001's "+ htmx" for interactivity.

## Context
The MVP screens were readable but looked like a collection of forms. The
navigation was a top row of eight links that wrapped to three lines on a
phone. The start page was a booking list, with no overview or trend. There
was no light/dark switch, only the OS preference. Plan 0002 schedules a
redesign that should feel like a finance tool.

ADR 0001 picked htmx for interactivity. After five phases, htmx still was not
needed. Every screen works as a plain form post. The interactions that plan
0002 adds are:

- adding invoice positions (clone a row),
- confirming a Storno,
- switching the theme,
- submitting a filter when a select changes.

None of them needs a server round trip for a partial page. htmx earns its
place when HTML fragments come back from the server, and none of these does
that.

## Decision

### A small design system in one stylesheet
`web/static/app.css` defines tokens (colours, type, radii, shadows) on `:root`,
and every component uses the tokens. Direction: a calm ledger. Warm paper in
light mode, deep ink in dark mode, one pine accent, and a brass highlight for
the one thing on screen that matters most. Big figures are set in a serif face
with tabular lining numerals, and amounts in lists use a tabular monospace, so
columns of numbers line up.

No web fonts: the app is self-hosted and may run offline on a LAN. The font
stacks are chosen per platform (Avenir Next / Iowan Old Style on Apple, Segoe
/ Palatino Linotype on Windows, Noto / P052 on Linux).

### Themes: System, Hell, Dunkel
The dark palette applies under `prefers-color-scheme: dark` unless
`<html data-theme="light">` overrides it, and always under
`data-theme="dark"`. The choice is stored in `localStorage` under `theme`, and
"System" removes the key. The switch cycles System → Hell → Dunkel.

The choice is per browser, not per user, and is not stored on the server.
There are no user accounts (ADR 0017), and a per-device preference is the
normal expectation for this.

### App shell
- Desktop (≥ 64rem): a sticky ink sidebar grouped into Tagesgeschäft /
  Auswertung / Stammdaten, with a brass "Buchung erfassen" button at the top.
- Phone: a sticky top bar (brand, theme switch, a `<details>` "Mehr" menu) and
  a bottom tab bar with the five most frequent destinations, plus a central
  "+" button to book.
- `render` marks the current navigation item from the page template's name
  (`navFor`), so handlers do not repeat it.

### One script, loaded in `<head>`, entirely optional
`web/static/app.js` sets the theme before first paint (so it is not
deferred), then adds four enhancements: the theme switch, Storno confirmation,
invoice "Position hinzufügen" (it clones a `<template>`), and auto-submitting
filter selects. The service-worker registration moved here from an inline
`<script>`.

**Without the script every page still works.** The theme follows the OS, a
Storno submits without asking, an invoice has three blank rows (more after a
re-render), and filters submit with the button.

**No inline scripts or style attributes** anywhere, so the CSP in ADR 0017 can
say `script-src 'self'; style-src 'self'`. A test enforces this on the main
pages.

### Charts are server-rendered SVG
The overview's monthly chart and the Kleinunternehmer meter are computed in
Go (`internal/server/chart.go`, tested) and drawn as inline SVG with
presentation attributes. That means no charting library, nothing to download,
and they theme through CSS classes.

### The service worker goes network-first for the shell
Cache-first meant an upgraded app kept serving the stylesheet the worker was
installed with. That was found while checking the redesign in the browser:
new markup, old CSS. The cache is now only an offline fallback. Pages remain
network-only, as ADR 0011 decided.

## Consequences
- ADR 0001's "+ htmx" is superseded. If a real partial-update need appears
  (for example live match suggestions while typing), htmx is still the
  preferred tool, vendored as a static file rather than loaded from a CDN.
- There is still no build step and no node toolchain.
- Theme preference is per device.
- Static assets are embedded (`go:embed`), so a CSS change needs a restart of
  `make dev`. That was already true.

## Alternatives considered
- **htmx now, for consistency with ADR 0001:** 14 kB for four behaviours that
  vanilla JS does in about 100 lines. It could be revisited, see above.
- **A CSS framework (Tailwind, Pico):** Tailwind needs a build step for a
  sane bundle, and Pico gives a generic look. Neither fits "boring and no
  build step" and a distinct look at the same time.
- **Theme stored server-side in a cookie:** avoids the pre-paint script, but
  adds a request round trip for a toggle and still needs script for the
  button. It is not worth it.
- **A charting library (Chart.js via CDN):** needs a network dependency and a
  CSP exception for a single bar chart.
