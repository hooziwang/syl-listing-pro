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
DEFAULT_GOAL := default

INPUTS ?=
OUTPUT ?=
NUM ?=
VERBOSE ?=
LOG_FILE ?=

.DEFAULT_GOAL := $(DEFAULT_GOAL)

.PHONY: default help build test fmt tidy clean run run-gen install uninstall

default:
	@$(MAKE) fmt
	@$(MAKE) test
	@$(MAKE) install

help:
	@echo "Targets:"
	@echo "  make              - 默认流程：fmt -> test -> install"
	@echo "  make build        - 编译二进制到 $(BIN)"
	@echo "  make test         - 运行全部测试"
	@echo "  make fmt          - gofmt 全部 Go 文件"
	@echo "  make tidy         - 整理 go.mod/go.sum"
	@echo "  make run          - 直跑入口（需要 INPUTS）"
	@echo "  make run-gen      - gen 子命令入口（需要 INPUTS）"
	@echo "  make install      - 安装到 Go bin 目录（GOBIN 或 GOPATH/bin）"
	@echo "  make uninstall    - 卸载已安装二进制"
	@echo "  make clean        - 删除构建产物"
	@echo ""
	@echo "Variables:"
	@echo "  INPUTS='/path/a.md /path/b.md'   必填（run/run-gen）"
	@echo "  OUTPUT=/path/out_dir              可选，对应 -o"
	@echo "  NUM=3                             可选，对应 -n"
	@echo "  VERBOSE=1                         可选，启用 --verbose"
	@echo "  LOG_FILE=/path/run.log            可选，对应 --log-file"
	@echo "  GO_BIN_DIR=...                    覆盖安装目录（默认 GOBIN 或 GOPATH/bin）"
	@echo "  DESTDIR=                          打包场景根目录"
	@echo "  VERSION=v0.1.0                    可选，覆盖版本号"
	@echo "  COMMIT=abc1234                    可选，覆盖提交哈希"
	@echo "  BUILD_TIME=...                    可选，覆盖构建时间（UTC）"

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) .

test:
	$(GO) test ./...

fmt:
	@gofmt -w $$(find . -name '*.go' -type f)

tidy:
	$(GO) mod tidy

run:
	@if [ -z "$(INPUTS)" ]; then echo "还没传 INPUTS 输入文件或目录"; exit 1; fi
	$(GO) run . $(INPUTS) $(if $(OUTPUT),-o "$(OUTPUT)",) $(if $(NUM),-n $(NUM),) $(if $(VERBOSE),--verbose,) $(if $(LOG_FILE),--log-file "$(LOG_FILE)",)

run-gen:
	@if [ -z "$(INPUTS)" ]; then echo "还没传 INPUTS 输入文件或目录"; exit 1; fi
	$(GO) run . gen $(INPUTS) $(if $(OUTPUT),-o "$(OUTPUT)",) $(if $(NUM),-n $(NUM),) $(if $(VERBOSE),--verbose,) $(if $(LOG_FILE),--log-file "$(LOG_FILE)",)

clean:
	rm -rf $(BIN_DIR)

install: build
	@mkdir -p "$(INSTALL_BIN_DIR)"
	install -m 0755 "$(BIN)" "$(INSTALL_BIN)"

uninstall:
	rm -f "$(INSTALL_BIN)"
