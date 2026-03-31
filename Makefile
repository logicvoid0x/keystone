SHELL := bash
GO ?= go
BUILDDIR ?= build

.PHONY: all build test lint proto proto-tools proto-lint check-go e2e localnet-start localnet-stop clean

all: build

# The SDK does not build for 32-bit targets; catch a wrong Go on PATH early.
check-go:
	@arch=$$($(GO) env GOARCH); if [ "$$arch" != "amd64" ] && [ "$$arch" != "arm64" ]; then \
		echo "error: $(GO) targets GOARCH=$$arch; use a 64-bit Go (see docs/status.md)"; exit 1; fi

build: check-go
	@mkdir -p $(BUILDDIR)
	$(GO) build -o $(BUILDDIR)/ ./cmd/keystoned

test: check-go
	GO=$(GO) bash scripts/gotest.sh ./...

lint: check-go
	$(GO) vet ./...
	$(GO) run ./tools/detlint/cmd/detlint ./...
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
		else echo "golangci-lint not installed; skipped"; fi

proto-tools:
	$(GO) install github.com/cosmos/gogoproto/protoc-gen-gocosmos@v1.7.2
	$(GO) install github.com/grpc-ecosystem/grpc-gateway/protoc-gen-grpc-gateway@v1.16.0

proto:
	bash scripts/protocgen.sh

proto-lint:
	cd proto && buf lint

e2e: build
	bash scripts/demo-M0.sh

localnet-start:
	@echo "localnet arrives in M2"; exit 1

localnet-stop:
	@echo "localnet arrives in M2"; exit 1

clean:
	rm -rf $(BUILDDIR)
