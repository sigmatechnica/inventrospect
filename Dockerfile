# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.2-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X inventrospect/internal/buildinfo.Version=$VERSION -X inventrospect/internal/buildinfo.Commit=$COMMIT -X inventrospect/internal/buildinfo.Date=$BUILD_DATE" -o /out/inventrospect ./cmd/server && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X inventrospect/internal/buildinfo.Version=$VERSION -X inventrospect/internal/buildinfo.Commit=$COMMIT -X inventrospect/internal/buildinfo.Date=$BUILD_DATE" -o /out/inventrospect-mcp ./cmd/mcp && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X inventrospect/internal/buildinfo.Version=$VERSION -X inventrospect/internal/buildinfo.Commit=$COMMIT -X inventrospect/internal/buildinfo.Date=$BUILD_DATE" -o /out/inventrospect-ops ./cmd/ops && \
    mkdir -p /out/data /out/backups && chmod 0700 /out/data /out/backups

FROM gcr.io/distroless/static-debian13:nonroot
ARG VERSION=dev
ARG COMMIT=unknown
ARG SOURCE_URL=https://github.com/sigmatechnica/inventrospect
LABEL org.opencontainers.image.title="Inventrospect" \
      org.opencontainers.image.description="Self-hosted engineering inventory with web, REST and MCP interfaces" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$COMMIT \
      org.opencontainers.image.source=$SOURCE_URL
COPY --from=build /out/inventrospect /out/inventrospect-mcp /out/inventrospect-ops /usr/local/bin/
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/backups /backups
USER 65532:65532
WORKDIR /data
EXPOSE 8080 8081
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=3s --start-period=20s --retries=3 CMD ["/usr/local/bin/inventrospect-ops", "health"]
ENTRYPOINT ["/usr/local/bin/inventrospect"]
CMD ["-data", "/data", "-addr", "127.0.0.1:8080"]
