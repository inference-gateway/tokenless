# syntax=docker/dockerfile:1
# Multi-platform build: buildx compiles natively on the runner ($BUILDPLATFORM)
# and cross-compiles for each target via GOOS/GOARCH, so no emulation is needed.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags "-w -s -X main.version=${VERSION}" -o /out/tokenless ./cmd/tokenless

FROM scratch
LABEL org.opencontainers.image.source=https://github.com/inference-gateway/tokenless
COPY --from=build /out/tokenless /tokenless
ENTRYPOINT ["/tokenless"]
