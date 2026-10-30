# Deliberately plain Dockerfile syntax — no BuildKit-only features (cache
# mounts, heredocs), so `podman build` and `docker build` behave identically
# (ADR 0005).

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
# CGO_ENABLED=0 is what makes modernc.org/sqlite (pure Go, ADR 0001) pay off:
# a fully static binary with no libc coupling.
#
# GOARCH is deliberately NOT pinned: it defaults to the build platform, which is
# also what the runtime `FROM alpine` below resolves to. Hardcoding amd64 would
# produce an amd64 binary on an arm64 base when built on an Apple Silicon
# machine — an image that cannot exec. The amd64-only release (ADR 0005) comes
# from the CI runner being amd64, and release.yaml asserts that before pushing.
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/bookkeeper ./cmd/bookkeeper

FROM alpine:3.21
ARG VERSION=dev
LABEL org.opencontainers.image.title="kmu-buchhaltung" \
      org.opencontainers.image.description="Bookkeeping for small Austrian businesses" \
      org.opencontainers.image.source="https://github.com/chrthal/accountant" \
      org.opencontainers.image.version="${VERSION}"
# tzdata is not optional: Austrian booking dates must not shift under UTC.
#
# tesseract powers the receipt OCR (ADR 0011). It roughly quadruples the image,
# from ~27 MB to ~100 MB, which is the honest price of the feature — the app
# degrades to manual entry without it, so anyone who would rather have the small
# image can drop these two packages and lose only the pre-fill.
#
# poppler-utils (pdftotext, pdftoppm) reads supplier PDF invoices (ADR 0018):
# the text layer directly, scans via tesseract. Optional in the same way.
# English language data as well: invoices from software and hosting
# providers are often English, and ocr.Languages uses deu+eng when both exist.
RUN apk add --no-cache ca-certificates tzdata wget \
        tesseract-ocr tesseract-ocr-data-deu tesseract-ocr-data-eng poppler-utils && \
    adduser -D -u 10001 app && \
    mkdir -p /data /backups && chown app /data /backups
COPY --from=build /out/bookkeeper /usr/local/bin/bookkeeper
USER app
# Default to the zero-setup SQLite path so `docker run` works with nothing else
# running; a real deployment overrides this with a postgres:// DSN (ADR 0006).
ENV BOOKKEEPER_DSN=sqlite:///data/bookkeeper.db
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["bookkeeper"]
CMD ["-addr", ":8080"]
