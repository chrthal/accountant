# ADR 0005: Build & release — containers, GitHub Actions, GitHub Container Registry

**Date:** 2026-08-17
**Status:** Accepted

## Context
ADR 0001 committed to shipping a single static binary plus a SQLite file, and
noted the thing should be containerizable later if it ever needs to move off a
single host. "Later" is now: the project needs a repeatable local test
environment and an automated way to produce a deployable artifact, before
feature code exists rather than retrofitted onto a half-built app.

The code is hosted on GitHub, which carries both Actions and a container
registry (ghcr.io). Using them costs nothing extra to operate — no second
service to host and no extra account.

Where the released image actually runs — a single container on a host, a
compose stack, or k8s — is left to whoever deploys it and does not need to be
decided in order to build the artifact.

## Decision

**Artifact:** one OCI container image, `ghcr.io/chrthal/accountant`
(the registry needs lowercase names). Built from a
two-stage Dockerfile: `golang:1.24-alpine` builder, `alpine:3.21` runtime,
`CGO_ENABLED=0`. The SQLite file lives on a volume at `/data`; the container
holds no state of its own.

**Architecture:** `linux/amd64` only. Multi-arch would mean QEMU or buildx
cross-builds in CI for a target nobody has asked for; adding `arm64` later is a
one-line change to the build step when there is a real arm64 host.

**Local environment: runtime-agnostic.** The Dockerfile avoids BuildKit-only
syntax and `compose.yaml` avoids Docker-only features and bind mounts, so the
same files work under `docker compose` and under rootless `podman`. A `Makefile`
detects whichever runtime is installed.

**Runtime base: alpine, not distroless.** Distroless is smaller and has less
attack surface, but gives up a shell and `wget` — which costs a working
`HEALTHCHECK` and the ability to debug a misbehaving container in place. For a
self-hosted single-user bookkeeping tool that trade goes the other way. `tzdata`
is installed deliberately: Austrian booking dates must not shift under UTC.

**Pipelines:** `.github/workflows/ci.yaml` on push/PR runs vet + test + build.
`.github/workflows/release.yaml` on a `v*` tag builds, pushes `:<version>` and
`:latest`, and opens a GitHub release. Both use **plain `docker` CLI steps** —
not `docker/build-push-action` or `setup-buildx-action`. Those actions add a
version-pinning chore to what is three shell commands, and amd64-only means
there is nothing for buildx to do.

**Versioning:** git tag `vX.Y.Z` is the single source of truth. The version is
stamped into the binary at build time via
`-ldflags "-X main.version=…"`, so a running container can be asked what it is.

## Consequences
+ One command (`make up`) gives a production-shaped environment locally; the
  image CI publishes is built from the same Dockerfile the developer uses.
+ `make test-container` runs the identical vet+test invocation CI runs, so
  "works on my machine" and "passes CI" converge.
+ Registry, CI, and source live in one system; the built-in `GITHUB_TOKEN`
  is enough to push the image, so no secrets need to be set up.
+ Deploy target stays open: an OCI image runs under compose, podman, or k8s
  unchanged.
- The workflows need `packages: write` permission to push to ghcr.io; it is
  set per job in the workflow files.
- Alpine's musl is fine here only because `modernc.org/sqlite` (ADR 0001) is
  pure Go and the binary is fully static. Any future cgo dependency breaks this
  and forces a base-image decision.

## Alternatives considered
- **A separate CI service or registry:** a second system and account for no
  gain over what the code host already provides.
- **Binary-only releases (attach static binaries to the release page):**
  simpler, but leaves every deploy target to solve process supervision, file
  layout, and dependencies itself. The image subsumes it; binaries can be added
  as extra release assets later if a use case appears.
- **Distroless / scratch base:** rejected above — no healthcheck, no debugging.
- **Multi-arch from day one:** rejected as speculative; no arm64 host exists.
