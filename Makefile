UI_DIR := ui
UI_STAMP := $(UI_DIR)/node_modules/.package-lock.json
STATIC_DIR := internal/webui/static
BIN_DIR := bin
API_BIN := $(BIN_DIR)/rookery
VNF_BIN := $(BIN_DIR)/vnfs
PROVIDER_BIN := $(BIN_DIR)/terraform-provider-rookery
VIRTIOFSD_BIN := extra/virtiofsd/virtiofsd
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PROVIDER_VERSION := $(patsubst v%,%,$(VERSION))
PROVIDER_RELEASE_NAME := terraform-provider-rookery_$(PROVIDER_VERSION)_linux_amd64
RELEASE_DIR := dist
RELEASE_NAME := rookery-$(VERSION)-linux-amd64
RELEASE_STAGE := $(RELEASE_DIR)/$(RELEASE_NAME)
RELEASE_ARCHIVE := $(RELEASE_DIR)/$(RELEASE_NAME).tar.gz
RELEASE_DOC_IMAGES := \
	docs/imgs/vms.png \
	docs/imgs/disks.png \
	docs/imgs/installation-media.png \
	docs/imgs/networks.png \
	docs/imgs/vnfs.png \
	docs/imgs/network-troubleshoot.png \
	docs/imgs/icon.png

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

.PHONY: all build ui-install ui-build ui-typecheck ui-watch test vet fmt clean release proto-tools proto-check-tools proto-generate proto-compile proto-check virtiofsd

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
	go build -o $(API_BIN) ./cmd/rookery
	go build -o $(VNF_BIN) ./cmd/vnfs
	go build -o $(PROVIDER_BIN) ./cmd/terraform-provider-rookery

# Download virtiofsd into extra/virtiofsd for local runs and release packaging.
virtiofsd:
	$(MAKE) -C extra/virtiofsd virtiofsd

# Build the x86_64 Linux API, VNF daemon, and Terraform provider archive
# published by the release workflow.
release: ui-build virtiofsd
	rm -rf $(RELEASE_STAGE) $(RELEASE_ARCHIVE) $(RELEASE_ARCHIVE).sha256
	mkdir -p $(RELEASE_STAGE)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o $(RELEASE_STAGE)/rookery ./cmd/rookery
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o $(RELEASE_STAGE)/vnfs ./cmd/vnfs
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$(PROVIDER_VERSION)" -o "$(RELEASE_STAGE)/$(PROVIDER_RELEASE_NAME)" ./cmd/terraform-provider-rookery
	cd $(RELEASE_STAGE) && sha256sum "$(PROVIDER_RELEASE_NAME)" > "$(PROVIDER_RELEASE_NAME).sha256"
	install -m 0755 $(VIRTIOFSD_BIN) $(RELEASE_STAGE)/virtiofsd
	install -m 0644 README.md config.example.yaml LICENSE NOTICE THIRD_PARTY_NOTICES.md $(RELEASE_STAGE)/
	install -d $(RELEASE_STAGE)/docs/imgs $(RELEASE_STAGE)/scripts
	install -m 0644 docs/*.md $(RELEASE_STAGE)/docs/
	install -m 0644 $(RELEASE_DOC_IMAGES) $(RELEASE_STAGE)/docs/imgs/
	install -m 0644 scripts/README.md $(RELEASE_STAGE)/scripts/
	install -m 0755 scripts/lvm-thin-snapshot-disk.sh $(RELEASE_STAGE)/scripts/
	tar -C $(RELEASE_DIR) -czf $(RELEASE_ARCHIVE) $(RELEASE_NAME)
	cd $(RELEASE_DIR) && sha256sum $(RELEASE_NAME).tar.gz > $(RELEASE_NAME).tar.gz.sha256
	@printf 'created %s\n' "$(RELEASE_ARCHIVE)"

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
	rm -rf $(UI_DIR)/node_modules $(STATIC_DIR)/build $(BIN_DIR) $(RELEASE_DIR)
