# Download the upstream static x86_64 virtiofsd artifact used by LightVM.
#
# The binary is written to extra/virtiofsd/virtiofsd, which is gitignored.
# Root `make virtiofsd` and `make release` delegate to this target.

SHELL := /bin/sh
.DEFAULT_GOAL := virtiofsd

EXTRA_DIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))

# The default source is the upstream virtio-fs publish job for main, which
# provides a static x86_64 musl binary. Override VIRTIOFSD_URL and
# VIRTIOFSD_SHA256 for a pinned build.
VIRTIOFSD_REF ?= main
VIRTIOFSD_URL ?= https://gitlab.com/virtio-fs/virtiofsd/-/jobs/artifacts/$(VIRTIOFSD_REF)/download?job=publish
VIRTIOFSD_SHA256 ?=
VIRTIOFSD_DIR := $(EXTRA_DIR)
VIRTIOFSD_BIN := $(VIRTIOFSD_DIR)/virtiofsd

.PHONY: all virtiofsd clean

all: virtiofsd

virtiofsd: $(VIRTIOFSD_BIN)

$(VIRTIOFSD_BIN):
	@command -v curl >/dev/null || { echo "error: curl is required to download virtiofsd" >&2; exit 1; }
	@command -v unzip >/dev/null || { echo "error: unzip is required to extract virtiofsd" >&2; exit 1; }
	@mkdir -p "$(VIRTIOFSD_DIR)"
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
		echo "downloading virtiofsd from $(VIRTIOFSD_URL)"; \
		curl -fL --retry 3 --retry-connrefused -o "$$tmp/virtiofsd.zip" "$(VIRTIOFSD_URL)"; \
		unzip -q "$$tmp/virtiofsd.zip" -d "$$tmp/extract"; \
		found=$$(find "$$tmp/extract" -type f -name virtiofsd -print -quit); \
		test -n "$$found" || { echo "error: virtiofsd artifact did not contain a virtiofsd binary" >&2; exit 1; }; \
		if [ -n "$(VIRTIOFSD_SHA256)" ]; then \
			echo "$(VIRTIOFSD_SHA256)  $$found" | sha256sum -c - || exit 1; \
		fi; \
		install -m 0755 "$$found" "$(VIRTIOFSD_BIN)"; \
		version=$$("$(VIRTIOFSD_BIN)" --version 2>/dev/null | head -n 1 || true); \
		echo "virtiofsd: $(VIRTIOFSD_BIN) $${version}"

clean:
	rm -f "$(VIRTIOFSD_BIN)"
