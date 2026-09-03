GO ?= go
GOCACHE ?= /tmp/go-build
VERSION ?= dev
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

.PHONY: dist
dist:
	mkdir -p bin
	GOCACHE=$(GOCACHE) CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o bin/orbitald-linux-amd64 .
	GOCACHE=$(GOCACHE) CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o bin/orbitald-linux-arm64 .

.PHONY: hashgen
hashgen:
	for f in bin/orbitald*; do shasum -a 256 $$f > $$f.sha256; done

.PHONY: publish
publish: dist hashgen
