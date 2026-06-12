# 🐗 HOG - Highly Over-engineered Gateway

A KrakenD-CE fork with built-in **OAuth 2.0 / OIDC authentication**, **static content proxying**, and **enhanced observability** for modern SPA architectures.

## What is HOG?

HOG extends the ultra-high performance [KrakenD API Gateway](https://www.krakend.io) with production-ready authentication and observability features:

- **🔐 BFF Authentication** - OAuth 2.0 / OIDC plugin acting as a Backend-for-Frontend (PKCE, encrypted cookies, stateless sessions)
- **📦 Static Content Proxy** - Serve SPAs with optional per-route authentication and auto-redirect after login
- **📊 Enhanced Observability** - Structured JSON logging with OTEL/Datadog trace correlation
- **🚀 Zero-Trust by Default** - Secure cookie handling, XSS protection, and automatic JWT injection

## Quick Start

```yaml
# docker-compose.yaml
services:
  gateway:
    image: ghcr.io/paulopiriquito/hog:latest
    environment:
      - IDP_ISSUER=http://your-idp:5556
      - IDP_CLIENT_ID=your-client
      - IDP_CLIENT_SECRET=your-secret
      - AUTH_COOKIE_KEY=abcdefghijklmnopqrstuvwxyz123456 # must be exactly 32 bytes
    volumes:
      - ./krakend.json:/etc/krakend/krakend.json
    ports:
      - "8080:8080"
```

```json
{
  "version": 3,
  "extra_config": {
    "plugin/http-server": {
      "name": ["hog-authenticator", "hog-static-content"],
      "hog-authenticator": {
        "idp": { "type": "oidc" }
      },
      "hog-static-content": {
        "static": [
          { "path-prefix": "/*", "service-host": "http://spa:3000", "auth": true }
        ],
        "service-gateway": { "path-prefix": ["/api/*"] }
      }
    }
  },
  "endpoints": []
}
```

## HOG Plugins

### [hog-authenticator](./plugins/authenticator/README.md)
OAuth 2.0 / OIDC authentication plugin that acts as a Backend-for-Frontend (BFF):
- **OIDC Discovery** - Automatic endpoint configuration via `.well-known/openid-configuration`
- **PKCE Support** - Proof Key for Code Exchange for enhanced security
- **Encrypted Cookies** - AES-256-GCM encrypted HttpOnly cookies (XSS protection)
- **Stateless Sessions** - Horizontal scaling with signed state tokens
- **User Headers** - Automatic injection of `X-User-Id`, `X-User-Email`, `X-User-Name`

### [hog-static-content](./plugins/static-content/README.md)
Static content proxy with optional authentication:
- **Wildcard Routing** - Flexible path patterns (`/app/*`, `/assets/*`)
- **Multiple Upstreams** - Different static servers per path
- **Optional Auth** - Per-route authentication via `hog-authenticator`
- **Auto-Redirect** - Unauthenticated users redirected to login, then back to original path

## HOG Packages

Reusable Go packages for extending KrakenD:

| Package | Description |
|---------|-------------|
| [pkg/logging](./pkg/logging/) | Structured logging with trace context and access logs |
| [pkg/headers](./pkg/headers/) | Header manipulation and trace propagation utilities |
| [pkg/forward](./pkg/forward/) | Project IdP claims into forwarded headers (filter, rename, map) |
| [pkg/session](./pkg/session/) | Cookie and JWT session management |
| [pkg/paths](./pkg/paths/) | URL pattern matching for routing |
| [pkg/pluginlogger](./pkg/pluginlogger/) | Logger wrapper for KrakenD plugins |

## Documentation

### HOG Features
- [Observability & Trace Correlation](./docs/observability.md) - Configure JSON logging with OTEL/Datadog trace IDs
- [Authenticator Plugin](./plugins/authenticator/README.md) - Full BFF authentication configuration
- [Static Content Plugin](./plugins/static-content/README.md) - Static serving with auth options
- [Local Stack](./tests/local-stack/README.md) - Docker-based testing with Dex IdP and Grafana observability
- [End-to-End Tests](./tests/e2e/) - Headless-browser tests covering simple-auth, client PKCE, and protected static/API flows


This project is not supported by the KrakenD team.

---
# Recognition

This project is a fork of [KrakenD Community Edition](https://www.krakend.io)
Thank you to the KrakenD and Lura team for creating such a great piece of software.

## KrakenD Community Edition

KrakenD is an extensible, ultra-high performance API Gateway that helps you effortlessly adopt microservices and secure communications. KrakenD is easy to operate and run and scales out without a single point of failure.

**KrakenD Community Edition** (or *KrakenD-CE*) is the open-source distribution of [KrakenD](https://www.krakend.io).

[KrakenD Site](https://www.krakend.io/) | [Documentation](https://www.krakend.io/docs/overview/) | [Blog](https://www.krakend.io/blog/) | [Twitter](https://twitter.com/krakend_io) | [Downloads](https://www.krakend.io/download/)

### Benefits

- **Easy integration** of an ultra-high performance gateway.
- **Effortlessly transition to microservices** and Backend For Frontend implementations.
- **True linear scalability**: Thanks to its **stateless design**, every KrakenD node can operate independently in the cluster without any coordination or centralized persistence.
- **Low operational cost**: +70K reqs/s on a single instance of regular size. Super low memory consumption with high traffic (usually under 50MB w/ +1000 concurrent). Fewer machines. Smaller machines. Lower budget.
- **Platform-agnostic**. Whether you work in a Cloud-native environment (e.g., Kubernetes) or self-hosted on-premises.
- **No vendor lock-in**: Reuse the best existing open-source and proprietary tools rather than having everything in the gateway (telemetry, identity providers, etc.)
- **API Lifecycle**: Using **GitOps** and **declarative configuration**.
- **Decouple clients** from existing services. Create new APIs without changing your existing API contracts.

### Technical features

- **Content aggregation**, composition, and filtering: Create views and mashups of aggregated content from your APIs.
- **Content Manipulation and format transformation**: Change responses, convert transparently from XML to JSON, and vice-versa.
- **Security**: Zero-trust policy, CORS, OAuth, JWT, HSTS, clickjacking protection, HPKP, MIME-Sniffing prevention, XSS protection...
- **Concurrent calls**: Serve content faster than consuming backends directly.
- **SSL** and  **HTTP2** ready
- **Throttling**: Limits of usage in the router and proxy layers
- **Multi-layer rate-limiting** for the end-user and between KrakenD and your services, including bursting, load balancing, and circuit breaker.
- **Telemetry** and dashboards of all sorts: Datadog, Zipkin, Jaeger, Prometheus, Grafana...
- **Extensible** with Go plugins, Lua scripts, Martian, or Google CEL spec.

See the [website](https://www.krakend.io) for more information.

### Download
KrakenD is [packaged and distributed in several formats](https://www.krakend.io/download/). You don't need to clone this repo to use KrakenD unless you want to tweak and build the binary yourself.

### Run
In its simplest form with the [offical Docker image](https://hub.docker.com/_/krakend):

    docker run -it -p "8080:8080" krakend

Now see [http://localhost:8080/__health](http://localhost:8080/__health). The gateway is listening. Now *CTRL-C* and replace  `/etc/krakend/krakend.json` with your [first configuration](https://designer.krakend.io).

### Build
See the required Go version in the `Makefile`, and then:
```
make build
```

Or, if you don't have or don't want to install `go`, you can build it using the golang docker container:

```
make build_on_docker
```


### License

Apache Version 2.0 - ./LICENSE
