GO ?= go
GOCACHE ?= /tmp/go-build
VERSION ?= dev
DESTDIR ?=
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
CLIBINDIR ?= $(BINDIR)
UNITDIR ?= $(PREFIX)/lib/systemd/system
INSTALL ?= install
INSTALL_DEPS ?= auto
ORBITALD_USER ?=
ORBITALD_GROUP ?=
ORBITALD_STATE_DIR ?= /var/lib/orbitald
ORBITALD_SNAPSHOTTER ?= overlayfs
CONTAINERD_SOCK ?= /run/containerd/containerd.sock
CONTAINERD_GROUP ?= containerd
REMOVE_CONTAINERD ?= ask
PURGE_STATE ?= 0
LDFLAGS := -s -w -X github.com/orbitald/orbitald/internal/orbitald.Version=$(VERSION)

.PHONY: all
all: test dist

.PHONY: test
test:
	GOCACHE=$(GOCACHE) $(GO) test ./...

.PHONY: build
build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) $(GO) build -ldflags "$(LDFLAGS)" -o bin/orbitald .
	GOCACHE=$(GOCACHE) $(GO) build -ldflags "$(LDFLAGS)" -o bin/obd ./cmd/obd

.PHONY: install
install: build install-system

.PHONY: install-system
install-system:
	@DESTDIR="$(DESTDIR)" \
	PREFIX="$(PREFIX)" \
	BINDIR="$(BINDIR)" \
	CLIBINDIR="$(CLIBINDIR)" \
	UNITDIR="$(UNITDIR)" \
	INSTALL="$(INSTALL)" \
	INSTALL_DEPS="$(INSTALL_DEPS)" \
	ORBITALD_USER="$(ORBITALD_USER)" \
	ORBITALD_GROUP="$(ORBITALD_GROUP)" \
	ORBITALD_STATE_DIR="$(ORBITALD_STATE_DIR)" \
	ORBITALD_SNAPSHOTTER="$(ORBITALD_SNAPSHOTTER)" \
	CONTAINERD_SOCK="$(CONTAINERD_SOCK)" \
	CONTAINERD_GROUP="$(CONTAINERD_GROUP)" \
	sh hack/install.sh

.PHONY: uninstall
uninstall:
	@DESTDIR="$(DESTDIR)" \
	PREFIX="$(PREFIX)" \
	BINDIR="$(BINDIR)" \
	CLIBINDIR="$(CLIBINDIR)" \
	UNITDIR="$(UNITDIR)" \
	ORBITALD_STATE_DIR="$(ORBITALD_STATE_DIR)" \
	CONTAINERD_GROUP="$(CONTAINERD_GROUP)" \
	REMOVE_CONTAINERD="$(REMOVE_CONTAINERD)" \
	PURGE_STATE="$(PURGE_STATE)" \
	sh hack/uninstall.sh

.PHONY: dist
dist:
	mkdir -p bin
	GOCACHE=$(GOCACHE) CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o bin/orbitald-linux-amd64 .
	GOCACHE=$(GOCACHE) CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o bin/orbitald-linux-arm64 .
	GOCACHE=$(GOCACHE) CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o bin/obd-linux-amd64 ./cmd/obd
	GOCACHE=$(GOCACHE) CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o bin/obd-linux-arm64 ./cmd/obd

.PHONY: hashgen
hashgen:
	for f in bin/orbitald* bin/obd*; do [ -e "$$f" ] || continue; shasum -a 256 $$f > $$f.sha256; done

.PHONY: publish
publish: dist hashgen
