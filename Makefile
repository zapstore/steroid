# Deploy runs: make release REF=<ref>
# Writes dist/<name>-<ref>-<arch>. Infra installs that file as releases/<id>-<ref>.
# go-sqlite3 (fts5) and chai2010/webp both pass -lm. Apple ld warns; ignore it.

NAME := steroid
REF ?=
GOARCH ?= $(shell go env GOARCH)
DIST := dist/$(NAME)-$(or $(REF),dev)-$(GOARCH)

GOOS ?= $(shell go env GOOS)
ifeq ($(GOOS),darwin)
CGO_LDFLAGS += -Wl,-no_warn_duplicate_libraries
export CGO_LDFLAGS
endif

.PHONY: build release clean

build:
	mkdir -p dist
	CGO_ENABLED=1 go build -tags fts5 -trimpath -o dist/steroid ./cmd/steroid

release:
	mkdir -p dist
	rm -rf $(DIST)
	CGO_ENABLED=1 go build -tags fts5 -trimpath -ldflags '-s -w' -o $(DIST) ./cmd/steroid

clean:
	rm -rf dist
