# 1) Build the static binary.
FROM golang:1.26-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /3gpp-mcp ./cmd/3gpp-mcp

# 2) Build the database. By default the latest version of every spec across all
#    releases is baked in; pass --build-arg RELEASE=19 to restrict to a single
#    release, or --build-arg MAX_RELEASE=19 to cap the newest release while
#    keeping specs that have no version in it. LibreOffice is required for
#    --convert-doc / --convert-image but lives only in this stage, so it never
#    bloats the final image. Temp files are deleted as each spec is processed,
#    keeping disk usage low.
FROM golang:1.26-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS db-builder
ARG RELEASE=latest
ARG MAX_RELEASE=
RUN apt-get update \
    && apt-get install -y --no-install-recommends libreoffice ca-certificates sqlite3 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /3gpp-mcp /3gpp-mcp
# After the build, switch the database out of WAL mode: a read-only open of a
# WAL-mode database must create -shm/-wal sidecars next to it, which the
# non-root runtime user cannot do in the root-owned /. In DELETE mode the
# baked-in database is readable with no write access at all.
RUN if [ "${RELEASE}" = "latest" ] || [ -z "${RELEASE}" ]; then \
        set -- --latest; \
    else \
        set -- --release "${RELEASE}"; \
    fi \
    && if [ -n "${MAX_RELEASE}" ]; then set -- "$@" --max-release "${MAX_RELEASE}"; fi \
    && /3gpp-mcp build "$@" \
    --db /3gpp.db \
    --convert-doc \
    --convert-image \
    --timeout 120s \
    --scrape-workers 4 \
    && sqlite3 /3gpp.db 'PRAGMA wal_checkpoint(TRUNCATE); PRAGMA journal_mode=DELETE;'

# 3) Final image: just the binary, the baked-in database, and CA certificates
#    (needed for the HTTPS archive listing behind list_versions). On-demand
#    fetching of versions not in the prebuilt DB does NOT work out of the box:
#    it needs a writable cache and temp directory, which scratch lacks. To
#    enable it, mount a writable volume and set HOME (or XDG_CACHE_HOME) and
#    TMPDIR to point into it.
FROM scratch
COPY --from=db-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=go-builder /3gpp-mcp /3gpp-mcp
COPY --from=db-builder /3gpp.db /3gpp.db
USER 65532:65532
ENTRYPOINT ["/3gpp-mcp"]
CMD ["serve", "--db", "/3gpp.db"]
