# syntax=docker/dockerfile:1
# hog-builder: build-stage base — carries the hog source + the hog-build composer.
FROM golang:1.26-alpine AS hog-builder
RUN apk add --no-cache git
# kustomize — render HOG config from base + overlays during the build stage
ARG KUSTOMIZE_VERSION=v5.5.0
ARG TARGETARCH=amd64
RUN set -eux; \
    case "$TARGETARCH" in arm64) a=arm64;; *) a=amd64;; esac; \
    wget -qO- "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize/${KUSTOMIZE_VERSION}/kustomize_${KUSTOMIZE_VERSION}_linux_${a}.tar.gz" | tar -xz -C /usr/local/bin kustomize; \
    kustomize version
WORKDIR /src
COPY . .
ENV GOWORK=off CGO_ENABLED=0
RUN go build -o /usr/local/bin/hog-build ./cmd/hog-build
ENV HOG_SOURCE=/src
WORKDIR /work
# Operators: COPY their gateway.yaml (+ local plugins), then
#   RUN hog-build --config gateway.yaml -o /out/hog
