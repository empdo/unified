# Builds for the platform the image is for (amd64, arm64, ...) without
# emulation: the Go build cross-compiles and needs no C toolchain.
FROM --platform=$BUILDPLATFORM node:24-alpine AS ui
RUN npm install -g pnpm@12.15.0
WORKDIR /ui
COPY ui/package.json ui/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY ui/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /ui/dist ./ui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /unified .

FROM alpine:3.22
RUN apk add --no-cache wireguard-tools-wg iproute2 su-exec ca-certificates tzdata
COPY --from=build /unified /usr/local/bin/unified
COPY docker/entrypoint.sh /entrypoint.sh
ENV UNIFIED_CONFIG=/config/config.toml \
    UNIFIED_DATA_DIR=/config/data \
    UNIFIED_MEDIA_DIR=/media
VOLUME ["/config", "/media"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["/entrypoint.sh"]
