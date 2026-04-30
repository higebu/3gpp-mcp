FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS go-builder
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w" \
    -o /3gpp-mcp ./cmd/3gpp-mcp

FROM scratch
COPY --from=go-builder /3gpp-mcp /3gpp-mcp
COPY 3gpp.db /3gpp.db
ENTRYPOINT ["/3gpp-mcp"]
CMD ["serve", "--db", "/3gpp.db"]
