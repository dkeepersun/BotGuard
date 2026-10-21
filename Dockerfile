# syntax=docker/dockerfile:1
# BotGuard 服务端镜像：一个镜像包含 gateway、admin-web 和 botguard-ctl，默认运行 gateway。
# 发布工作流推送到 ghcr.io/dkeepersun/botguard；部署用 deploy/docker/compose.yml，见 docs/install.md。
#
#   docker build -t botguard .
#   docker build --build-arg GO_IMAGE=镜像站/golang:1.26-alpine --build-arg BASE_IMAGE=镜像站/alpine:3.22 .

ARG GO_IMAGE=golang:1.26-alpine
ARG BASE_IMAGE=alpine:3.22

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG GOPROXY=https://proxy.golang.org,direct
ENV CGO_ENABLED=0 GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    for p in gateway admin-web botguard-ctl; do \
      GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
        -ldflags "-s -w -X github.com/dkeepersun/BotGuard/internal/version.Version=${VERSION}" \
        -o /out/bin/$p ./cmd/$p || exit 1; \
    done
# 容器版配置：监听所有地址，日志输出到标准输出，Redis 地址取环境变量 BOTGUARD_REDIS_ADDR。
COPY configs ./configs
RUN mkdir -p /out/etc && for svc in gateway admin-web; do \
      sed -e 's|^Host:.*|Host: 0.0.0.0|' \
          -e '/^Log:/,/^[^[:space:]]/ s/^\([[:space:]]*Mode:\) file/\1 console/' \
          -e '/^Redis:/,/^[^[:space:]]/ s|^\([[:space:]]*Host:\).*|\1 ${BOTGUARD_REDIS_ADDR}|' \
          configs/$svc.yaml > /out/etc/$svc.yaml || exit 1; \
    done

# Alpine 自带 CA 证书；服务统一用 UTC，不需要额外安装软件包。
FROM ${BASE_IMAGE}
RUN addgroup -S -g 10001 botguard && \
    adduser -S -D -H -u 10001 -G botguard -h /var/lib/botguard botguard && \
    install -d -o botguard -g botguard -m 0750 /var/lib/botguard
COPY --from=build /out/bin/ /usr/local/bin/
COPY --from=build /out/etc/ /etc/botguard/
ENV BOTGUARD_DB_DRIVER=sqlite \
    BOTGUARD_DB_DSN=/var/lib/botguard/botguard.db \
    BOTGUARD_REDIS_ADDR=redis:6379
USER botguard
WORKDIR /var/lib/botguard
VOLUME /var/lib/botguard
EXPOSE 8090 8091
CMD ["gateway", "-f", "/etc/botguard/gateway.yaml"]
