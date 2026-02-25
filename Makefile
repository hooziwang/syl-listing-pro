APP := syl-listing-pro
GO ?= go
BIN_DIR ?= bin
BIN := $(BIN_DIR)/$(APP)
DESTDIR ?=
VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X 'syl-listing-pro/cmd.Version=$(VERSION)' -X 'syl-listing-pro/cmd.Commit=$(COMMIT)' -X 'syl-listing-pro/cmd.BuildTime=$(BUILD_TIME)'
GO_BIN_DIR ?= $(shell sh -c 'gobin="$$( $(GO) env GOBIN )"; if [ -n "$$gobin" ]; then printf "%s" "$$gobin"; else gopath="$$( $(GO) env GOPATH )"; printf "%s/bin" "$${gopath%%:*}"; fi')
INSTALL_BIN_DIR := $(DESTDIR)$(GO_BIN_DIR)
INSTALL_BIN := $(INSTALL_BIN_DIR)/$(APP)

.DEFAULT_GOAL := default

.PHONY: default help build test fmt tidy clean install uninstall

default:
	@$(MAKE) fmt
	@$(MAKE) test
	@$(MAKE) install

help:
	@echo "Targets: make | build | test | fmt | tidy | install | uninstall | clean"

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) .

test:
	$(GO) test ./...

fmt:
	@gofmt -w $$(find . -name '*.go' -type f)

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BIN_DIR)

install: build
	@mkdir -p "$(INSTALL_BIN_DIR)"
	install -m 0755 "$(BIN)" "$(INSTALL_BIN)"

uninstall:
	rm -f "$(INSTALL_BIN)"
