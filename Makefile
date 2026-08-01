PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
GOCACHE ?= $(CURDIR)/.cache/go-build

.PHONY: all build install test check clean

all: build

build:
	mkdir -p bin
	GOCACHE="$(GOCACHE)" go build -o bin/local-web-app-server ./cmd/local-web-app-server

install: build
	install -d "$(BINDIR)"
	install -m 0755 bin/local-web-app-server "$(BINDIR)/local-web-app-server"

test:
	GOCACHE="$(GOCACHE)" go test ./...

check:
	GOCACHE="$(GOCACHE)" go test ./...
	GOCACHE="$(GOCACHE)" go test -race ./...
	GOCACHE="$(GOCACHE)" go vet ./...
	GOCACHE="$(GOCACHE)" go build ./...

clean:
	rm -rf bin .cache
