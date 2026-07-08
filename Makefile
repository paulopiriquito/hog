# HOG v2 — developer + CI entry points.
GO      ?= go
DOCKER  ?= docker
REGISTRY ?= ghcr.io/paulopiriquito
TAG     ?= dev
COMPOSE  = $(DOCKER) compose -f tests/e2e/docker-compose.yaml

.PHONY: build test race vet fmt tidy vuln ci all images e2e e2e-up e2e-down docs docs-serve clean

build: ## build the binaries (emitted at the repo root; gitignored)
	$(GO) build -o hog ./cmd/hog
	$(GO) build -o hog-build ./cmd/hog-build

test: ## unit + integration tests
	$(GO) test ./...

race: ## tests under the race detector
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt: ## fail if any Go file is not gofmt-clean
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

tidy:
	$(GO) mod tidy

vuln: ## known-vulnerability scan
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

ci: fmt vet test race ## the unit gate CI runs

all: build test

images: ## build the four images locally (runtime -> static -> docs, + builder)
	$(DOCKER) build -f build/Dockerfile.builder -t $(REGISTRY)/hog-builder:$(TAG) .
	$(DOCKER) build -f build/Dockerfile.runtime -t $(REGISTRY)/hog-runtime:$(TAG) .
	$(DOCKER) build -f build/Dockerfile.static  -t $(REGISTRY)/hog-static:$(TAG) --build-arg BASE=$(REGISTRY)/hog-runtime:$(TAG) .
	$(DOCKER) build -f website/Dockerfile       -t $(REGISTRY)/hog-docs:$(TAG)   --build-arg BASE=$(REGISTRY)/hog-static:$(TAG) website

e2e: ## bring the stack up, run the e2e suite, tear down
	$(COMPOSE) up --build -d
	@trap '$(COMPOSE) down -v' EXIT; \
	(cd tests/e2e && GOWORK=off $(GO) test -timeout 600s ./...)

e2e-up:
	$(COMPOSE) up --build -d
e2e-down:
	$(COMPOSE) down -v

docs: ## strict docs build
	cd website && mkdocs build --strict
docs-serve:
	cd website && mkdocs serve

clean:
	rm -f hog hog-build
