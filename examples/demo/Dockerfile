# syntax=docker/dockerfile:1
# Build context is the repository root (see docker-compose.yml).

FROM golang:1.26 AS build
ARG GOPROXY=https://proxy.golang.org,direct
ENV CGO_ENABLED=0 GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -o /out/gateway ./cmd/gateway && \
    go build -trimpath -o /out/botguard-ctl ./cmd/botguard-ctl

# The gateway is a static binary; nothing else is needed at runtime.
FROM scratch AS gateway
COPY --from=build /out/gateway /gateway
COPY examples/demo/gateway.yaml /gateway.yaml
EXPOSE 8090
ENTRYPOINT ["/gateway", "-f", "/gateway.yaml"]

# The PHP demo site; also runs the one-off seed job with botguard-ctl.
FROM php:8.3-cli-alpine AS php
COPY --from=build /out/botguard-ctl /usr/local/bin/botguard-ctl
COPY packages/php /sdk/php
COPY packages/browser/src /app/public/botguard
COPY examples/demo/php /app
COPY examples/demo/seed.sh /seed.sh
ENV PHP_CLI_SERVER_WORKERS=4
WORKDIR /app
EXPOSE 8080
CMD ["php", "-S", "0.0.0.0:8080", "-t", "/app/public"]
