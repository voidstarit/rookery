UI_DIR := ui
UI_STAMP := $(UI_DIR)/node_modules/.package-lock.json
STATIC_DIR := internal/webui/static
BIN_DIR := bin
API_BIN := $(BIN_DIR)/nerds-vms-api

.PHONY: all build ui-install ui-build ui-typecheck ui-watch test vet fmt clean

all: build

$(UI_STAMP): $(UI_DIR)/package.json $(UI_DIR)/package-lock.json
	cd $(UI_DIR) && npm ci

ui-install: $(UI_STAMP)

ui-build: $(UI_STAMP)
	cd $(UI_DIR) && npm run build

ui-typecheck: $(UI_STAMP)
	cd $(UI_DIR) && npm run typecheck

ui-watch: $(UI_STAMP)
	cd $(UI_DIR) && npm run watch

build: ui-build
	mkdir -p $(BIN_DIR)
	go build -o $(API_BIN) ./cmd/nerds-vms-api

test: ui-build
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './ui/*')

clean:
	rm -rf $(UI_DIR)/node_modules $(STATIC_DIR)/build $(BIN_DIR)
