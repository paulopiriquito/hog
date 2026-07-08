# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY main.go .
RUN go mod init backend && CGO_ENABLED=0 go build -o /backend .
FROM alpine:3.21
COPY --from=build /backend /backend
EXPOSE 9000
ENTRYPOINT ["/backend"]
