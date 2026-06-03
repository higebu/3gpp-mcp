# 1) Build the static binary.
FROM golang:1.26-bookworm AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /3gpp-mcp ./cmd/3gpp-mcp

# 2) Build the database for the requested release. LibreOffice is required for
#    --convert-doc / --convert-image but lives only in this stage, so it never
#    bloats the final image. Temp files are deleted as each spec is processed,
#    keeping disk usage low.
FROM golang:1.26-bookworm AS db-builder
ARG RELEASE=19
RUN apt-get update \
    && apt-get install -y --no-install-recommends libreoffice ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /3gpp-mcp /3gpp-mcp
RUN /3gpp-mcp build \
    --release ${RELEASE} \
    --db /3gpp.db \
    --convert-doc \
    --convert-image \
    --timeout 120s \
    --scrape-workers 4

# 3) Final image: just the binary and the baked-in database.
FROM scratch
COPY --from=go-builder /3gpp-mcp /3gpp-mcp
COPY --from=db-builder /3gpp.db /3gpp.db
ENTRYPOINT ["/3gpp-mcp"]
CMD ["serve", "--db", "/3gpp.db"]
