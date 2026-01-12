ARG GOLANG_VERSION
ARG ALPINE_VERSION

FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} as builder

RUN apk --no-cache --virtual .build-deps add make gcc musl-dev binutils-gold git

COPY . /app
WORKDIR /app

# Build KrakenD binary
RUN make all


FROM alpine:${ALPINE_VERSION}
ARG KRAKEND_VERSION

LABEL core_maintainer="community@krakend.io"
LABEL maintainer="paulo.piriquito@outlook.pt"
LABEL krakend_version="${KRAKEND_VERSION}"

RUN apk upgrade --no-cache --no-interactive && apk add --no-cache ca-certificates tzdata && \
    adduser -u 1000 -S -D -H krakend && \
    mkdir -p /etc/krakend/plugins && \
    echo '{ "version": 3 }' > /etc/krakend/krakend.json

COPY --from=builder /app/krakend /usr/bin/krakend
COPY --from=builder /app/plugins/static-content/hog-static-content.so /etc/krakend/plugins/
COPY --from=builder /app/plugins/authenticator/hog-authenticator.so /etc/krakend/plugins/

USER 1000

WORKDIR /etc/krakend

ENV USAGE_DISABLE=1
ENV KRAKEND_PORT=8080

ENTRYPOINT [ "/usr/bin/krakend" ]
CMD [ "run", "-c", "/etc/krakend/krakend.json" ]

EXPOSE 8000 8090
