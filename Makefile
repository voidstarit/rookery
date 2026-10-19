UI_DIR := ui
UI_STAMP := $(UI_DIR)/node_modules/.package-lock.json
STATIC_DIR := internal/webui/static
BIN_DIR := bin
API_BIN := $(BIN_DIR)/nerds-vms-api

PROTOC ?= protoc
PROTO_ROOT := design/proto
PROTO_GEN := design/gen
PROTO_OPENAPI := design/openapi
PROTO_INCLUDE ?= /usr/include
PROTO_INCLUDES := -I $(PROTO_ROOT) -I design/third_party -I $(PROTO_INCLUDE)
PROTO_PLUGIN_BIN := $(shell go env GOPATH)/bin
PROTO_FILES := $(shell find $(PROTO_ROOT) -name '*.proto' -print)
PROTO_TOOL_PATHS := $(PROTO_PLUGIN_BIN)/protoc-gen-go $(PROTO_PLUGIN_BIN)/protoc-gen-go-grpc $(PROTO_PLUGIN_BIN)/protoc-gen-grpc-gateway $(PROTO_PLUGIN_BIN)/protoc-gen-openapiv2 $(PROTO_PLUGIN_BIN)/protoc-gen-validate
PROTO_TOOL_VERSIONS := google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12 google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2 github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.28.0 github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v2.28.0 github.com/envoyproxy/protoc-gen-validate@v1.3.3

.PHONY: all build ui-install ui-build ui-typecheck ui-watch test vet fmt clean proto-tools proto-check-tools proto-generate proto-compile proto-check

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

# Protobuf generation uses protoc directly. The generator binaries are pinned
# above and generated manifests are committed under design/gen and design/openapi.
proto-tools:
	@for tool in $(PROTO_TOOL_VERSIONS); do \
		echo "go install $$tool"; \
		go install "$$tool" || exit $$?; \
	done

proto-check-tools:
	@command -v $(PROTOC) >/dev/null || { echo "missing $(PROTOC)"; exit 1; }
	@test -f $(PROTO_INCLUDE)/google/protobuf/descriptor.proto || { echo "missing protobuf well-known types under $(PROTO_INCLUDE)"; exit 1; }
	@for tool in $(PROTO_TOOL_PATHS); do test -x "$$tool" || { echo "missing $$tool; run make proto-tools"; exit 1; }; done

proto-generate: proto-check-tools
	rm -rf $(PROTO_GEN)/rookery $(PROTO_OPENAPI)
	mkdir -p $(PROTO_GEN) $(PROTO_OPENAPI)
	PATH="$(PATH):$(PROTO_PLUGIN_BIN)" $(PROTOC) $(PROTO_INCLUDES) \
		--go_out=$(PROTO_GEN) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_GEN) --go-grpc_opt=paths=source_relative \
		--grpc-gateway_out=$(PROTO_GEN) --grpc-gateway_opt=paths=source_relative \
		--validate_out=lang=go,paths=source_relative:$(PROTO_GEN) \
		--openapiv2_out=$(PROTO_OPENAPI) --openapiv2_opt=allow_merge=true,merge_file_name=rookery,json_names_for_fields=false \
		$(PROTO_FILES)

proto-compile: proto-check-tools
	@tmp=$$(mktemp); trap 'rm -f "$$tmp"' EXIT; \
	PATH="$(PATH):$(PROTO_PLUGIN_BIN)" $(PROTOC) $(PROTO_INCLUDES) --descriptor_set_out="$$tmp" $(PROTO_FILES); \
	echo "protobuf compile OK"

proto-check: proto-generate proto-compile
	@git diff --exit-code -- $(PROTO_GEN) $(PROTO_OPENAPI)
	@test -z "$$(git ls-files --others --exclude-standard $(PROTO_GEN) $(PROTO_OPENAPI))" || { echo "uncommitted generated proto files"; exit 1; }
	@echo "protobuf manifests are up to date"

clean:
	rm -rf $(UI_DIR)/node_modules $(STATIC_DIR)/build $(BIN_DIR)
