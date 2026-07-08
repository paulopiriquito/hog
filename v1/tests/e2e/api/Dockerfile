FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY main.go .
RUN go mod init e2e-api && go build -o api-server .

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/api-server .
EXPOSE 8000
ENTRYPOINT ["./api-server"]
