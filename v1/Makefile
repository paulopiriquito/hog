.PHONY: all build test build-plugin test-plugin

# This Makefile is a simple example that demonstrates usual steps to build a binary that can be run in the same
# architecture that was compiled in. The "ldflags" in the build assure that any needed dependency is included in the
# binary and no external dependencies are needed to run the service.

BIN_NAME := krakend
OS := $(shell uname | tr '[:upper:]' '[:lower:]')
MODULE := github.com/paulopiriquito/hog
VERSION := 2.12.0
HOG_VERSION := 1.3.0
SCHEMA_VERSION := $(shell echo "${VERSION}" | cut -d '.' -f 1,2)
GIT_COMMIT := $(shell git rev-parse --short=7 HEAD)
PKGNAME := hog
LICENSE := Apache 2.0
VENDOR=
URL := http://krakend.io
RELEASE := 0
USER := krakend
ARCH := amd64
DESC := High
MAINTAINER := Paulo Piriquito
DOCKER_WDIR := /tmp/fpm
DOCKER_FPM := paulopiriquito/fpm
GOLANG_VERSION := 1.26
GLIBC_VERSION := $(shell sh find_glibc.sh)
ALPINE_VERSION := 3.21
# Builder base image. golang is not published as -alpine${ALPINE_VERSION} for
# every alpine release (e.g. golang:1.26-alpine3.21 does not exist), so use the
# floating -alpine tag; the runtime stage still pins alpine:${ALPINE_VERSION}.
GOLANG_IMAGE := golang:${GOLANG_VERSION}-alpine
OS_TAG :=
EXTRA_LDFLAGS :=

# Plugin configuration
STATIC_PLUGIN_DIR := plugins/static-content
STATIC_PLUGIN_NAME := hog-static-content
STATIC_PLUGIN_OUTPUT := $(STATIC_PLUGIN_DIR)/$(STATIC_PLUGIN_NAME).so

AUTH_PLUGIN_DIR := plugins/authenticator
AUTH_PLUGIN_NAME := hog-authenticator
AUTH_PLUGIN_OUTPUT := $(AUTH_PLUGIN_DIR)/$(AUTH_PLUGIN_NAME).so

FPM_OPTS=-s dir -v $(VERSION) -n $(PKGNAME) \
  --license "$(LICENSE)" \
  --vendor "$(VENDOR)" \
  --maintainer "$(MAINTAINER)" \
  --architecture $(ARCH) \
  --url "$(URL)" \
  --description  "$(DESC)" \
	--config-files etc/ \
  --verbose

DEB_OPTS= -t deb --deb-user $(USER) \
	--depends ca-certificates \
	--depends rsyslog \
	--depends logrotate \
	--before-remove builder/scripts/prerm.deb \
  --after-remove builder/scripts/postrm.deb \
	--before-install builder/scripts/preinst.deb

RPM_OPTS =--rpm-user $(USER) \
	--depends rsyslog \
	--depends logrotate \
	--before-install builder/scripts/preinst.rpm \
	--before-remove builder/scripts/prerm.rpm \
  --after-remove builder/scripts/postrm.rpm

all: test

build-static-plugin:
	@echo "Building plugin $(STATIC_PLUGIN_NAME)..."
	@cd $(STATIC_PLUGIN_DIR) && go get .
	@cd $(STATIC_PLUGIN_DIR) && go build -buildmode=plugin -o $(STATIC_PLUGIN_NAME).so .
	@echo "Plugin built successfully at $(STATIC_PLUGIN_OUTPUT)"

build-auth-plugin:
	@echo "Building plugin $(AUTH_PLUGIN_NAME)..."
	@cd $(AUTH_PLUGIN_DIR) && go get .
	@cd $(AUTH_PLUGIN_DIR) && go build -buildmode=plugin -o $(AUTH_PLUGIN_NAME).so .
	@echo "Plugin built successfully at $(AUTH_PLUGIN_OUTPUT)"

build-plugin: build-static-plugin build-auth-plugin

test-static-plugin: build-static-plugin
	@echo "Testing static-content plugin..."
	@cd $(STATIC_PLUGIN_DIR) && go test -v .

test-auth-plugin: build-auth-plugin
	@echo "Testing authenticator plugin..."
	@cd $(AUTH_PLUGIN_DIR) && go test -v .

test-plugin: test-static-plugin test-auth-plugin

build: build-plugin cmd/krakend-ce/schema/schema.json
	@echo "Building the binary..."
	@go get .
	@go build -ldflags="-X ${MODULE}/pkg.Version=${VERSION} -X github.com/luraproject/lura/v2/core.KrakendVersion=${VERSION} \
	-X github.com/luraproject/lura/v2/core.GlibcVersion=${GLIBC_VERSION} ${EXTRA_LDFLAGS}" \
	-o ${BIN_NAME} ./cmd/krakend-ce
	@echo "You can now use ./${BIN_NAME}"

test: build
	go test -v ./plugins/static-content
	go test -v ./plugins/authenticator
	go test -v ./pkg/headers
	go test -v ./pkg/paths
	go test -v ./pkg/logging
	go test -v ./tests

cmd/krakend-ce/schema/schema.json:
	@echo "Fetching v${SCHEMA_VERSION} schema"
	@wget -qO $@ https://raw.githubusercontent.com/krakend/krakend-schema/refs/heads/main/v${SCHEMA_VERSION}/krakend.json || wget -qO $@ https://krakend.io/schema/krakend.json

# Build KrakenD using docker (defaults to whatever the golang container uses)
build_on_docker: docker-builder-linux
	docker run --rm -it -v "${PWD}:/app" -w /app ghcr.io/paulopiriquito/hog/builder:${HOG_VERISION}-linux-generic sh -c "git config --global --add safe.directory /app && make -e build"

# Build the container using the Dockerfile (alpine)
docker:
	docker build --no-cache --build-arg GOLANG_IMAGE=${GOLANG_IMAGE} --build-arg GOLANG_VERSION=${GOLANG_VERSION} --build-arg ALPINE_VERSION=${ALPINE_VERSION} --build-arg KRAKEND_VERSION=${VERSION} -t ghcr.io/paulopiriquito/hog:${HOG_VERSION} .

docker-builder:
	docker build --no-cache --build-arg GOLANG_VERSION=${GOLANG_VERSION} --build-arg ALPINE_VERSION=${ALPINE_VERSION} -t ghcr.io/paulopiriquito/hog/builder:${HOG_VERISION} -f Dockerfile-builder .

docker-builder-linux:
	docker build --no-cache --build-arg GOLANG_VERSION=${GOLANG_VERSION} -t ghcr.io/paulopiriquito/hog/builder:${HOG_VERISION}-linux-generic -f Dockerfile-builder-linux .

benchmark:
	@mkdir -p bench_res
	@touch bench_res/${GIT_COMMIT}.out
	@docker run --rm -d --name hog -v "${PWD}/tests/fixtures:/etc/krakend" -p 8080:8080 ghcr.io/paulopiriquito/hog:${HOG_VERISION} run -dc /etc/krakend/bench.json
	@sleep 2
	@docker run --rm -it --link hog peterevans/vegeta sh -c \
		"echo 'GET http://krakend:8080/test' | vegeta attack -rate=0 -duration=30s -max-workers=300 | tee results.bin | vegeta report" > bench_res/${GIT_COMMIT}.out
	@docker stop hog
	@cat bench_res/${GIT_COMMIT}.out

security_scan:
	@mkdir -p sec_scan
	@touch sec_scan/${GIT_COMMIT}.out
	@docker run --rm -d --name hog -v "${PWD}/tests/fixtures:/etc/krakend" -p 8080:8080 ghcr.io/paulopiriquito/hog:${HOG_VERISION} run -dc /etc/krakend/bench.json
	@docker run --rm -it --link hog instrumentisto/nmap --script vuln krakend > sec_scan/${GIT_COMMIT}.out
	@docker stop hog
	@cat sec_scan/${GIT_COMMIT}.out

builder/skel/%/etc/init.d/krakend: builder/files/krakend.init
	mkdir -p "$(dir $@)"
	cp builder/files/krakend.init "$@"

builder/skel/%/usr/bin/krakend: krakend
	mkdir -p "$(dir $@)"
	cp krakend "$@"

builder/skel/%/etc/krakend/krakend.json: krakend.json
	mkdir -p "$(dir $@)"
	cp krakend.json "$@"

builder/skel/%/lib/systemd/system/krakend.service: builder/files/krakend.service
	mkdir -p "$(dir $@)"
	cp builder/files/krakend.service "$@"

builder/skel/%/usr/lib/systemd/system/krakend.service: builder/files/krakend.service
	mkdir -p "$(dir $@)"
	cp builder/files/krakend.service "$@"

builder/skel/%/etc/rsyslog.d/krakend.conf: builder/files/krakend.conf-rsyslog
	mkdir -p "$(dir $@)"
	cp builder/files/krakend.conf-rsyslog "$@"

builder/skel/%/etc/logrotate.d/krakend: builder/files/krakend-logrotate
	mkdir -p "$(dir $@)"
	cp builder/files/krakend-logrotate "$@"

.PHONY: tgz
tgz: builder/skel/tgz/usr/bin/krakend
tgz: builder/skel/tgz/etc/krakend/krakend.json
tgz: builder/skel/tgz/etc/init.d/krakend
	tar zcvf krakend_${VERSION}_${ARCH}${OS_TAG}.tar.gz -C builder/skel/tgz/ .

.PHONY: deb
deb: builder/skel/deb/usr/bin/krakend
deb: builder/skel/deb/etc/krakend/krakend.json
deb: builder/skel/deb/etc/rsyslog.d/krakend.conf
deb: builder/skel/deb/etc/logrotate.d/krakend
	docker run --rm -it -v "${PWD}:${DOCKER_WDIR}" -w ${DOCKER_WDIR} ${DOCKER_FPM}:deb -t deb ${DEB_OPTS} \
		--iteration ${RELEASE} \
		--deb-systemd builder/files/krakend.service \
		-C builder/skel/deb \
		${FPM_OPTS}

.PHONY: rpm
rpm: builder/skel/rpm/usr/lib/systemd/system/krakend.service
rpm: builder/skel/rpm/usr/bin/krakend
rpm: builder/skel/rpm/etc/krakend/krakend.json
rpm: builder/skel/rpm/etc/rsyslog.d/krakend.conf
rpm: builder/skel/rpm/etc/logrotate.d/krakend
	docker run --rm -it -v "${PWD}:${DOCKER_WDIR}" -w ${DOCKER_WDIR} ${DOCKER_FPM}:rpm -t rpm ${RPM_OPTS} \
		--iteration ${RELEASE} \
		-C builder/skel/rpm \
		${FPM_OPTS}

.PHONY: deb-release
deb-release: builder/skel/deb-release/usr/bin/krakend
deb-release: builder/skel/deb-release/etc/krakend/krakend.json
	/usr/local/bin/fpm -t deb ${DEB_OPTS} \
		--iteration ${RELEASE} \
		--deb-systemd builder/files/krakend.service \
		-C builder/skel/deb-release \
		${FPM_OPTS}

.PHONY: rpm-release
rpm-release: builder/skel/rpm-release/usr/lib/systemd/system/krakend.service
rpm-release: builder/skel/rpm-release/usr/bin/krakend
rpm-release: builder/skel/rpm-release/etc/krakend/krakend.json
	/usr/local/bin/fpm -t rpm ${RPM_OPTS} \
		--iteration ${RELEASE} \
		-C builder/skel/rpm-release \
		${FPM_OPTS}

.PHONY: clean
clean:
	rm -rf builder/skel/*
	rm -f ${BIN_NAME}
	rm -rf vendor/
	rm -f cmd/krakend-ce/schema/schema.json
	rm -f $(STATIC_PLUGIN_OUTPUT)
	rm -f $(AUTH_PLUGIN_OUTPUT)
