# 1) Build the static binary.
FROM golang:1.26-bookworm AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /3gpp-mcp ./cmd/3gpp-mcp

# 2) Build the database. By default the latest version of every spec across all
#    releases is baked in; pass --build-arg RELEASE=19 to restrict to a single
#    release. LibreOffice is required for --convert-doc / --convert-image but
#    lives only in this stage, so it never bloats the final image. Temp files are
#    deleted as each spec is processed, keeping disk usage low.
FROM golang:1.26-bookworm AS db-builder
ARG RELEASE=latest
RUN apt-get update \
    && apt-get install -y --no-install-recommends libreoffice ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /3gpp-mcp /3gpp-mcp
RUN if [ "${RELEASE}" = "latest" ] || [ -z "${RELEASE}" ]; then \
        set -- --latest; \
    else \
        set -- --release "${RELEASE}"; \
    fi \
    && /3gpp-mcp build "$@" \
    --db /3gpp.db \
    --convert-doc \
    --convert-image \
    --timeout 120s \
    --scrape-workers 4

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
