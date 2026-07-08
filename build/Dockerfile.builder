# syntax=docker/dockerfile:1
# hog-builder: build-stage base — carries the hog source + the hog-build composer.
FROM golang:1.26-alpine AS hog-builder
RUN apk add --no-cache git
WORKDIR /src
COPY . .
ENV GOWORK=off CGO_ENABLED=0
RUN go build -o /usr/local/bin/hog-build ./cmd/hog-build
ENV HOG_SOURCE=/src
WORKDIR /work
# Operators: COPY their gateway.yaml (+ local plugins), then
#   RUN hog-build --config gateway.yaml -o /out/hog
