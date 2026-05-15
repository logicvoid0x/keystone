SHELL := bash
GO ?= go
BUILDDIR ?= build
# make test builds with these tags (spec M1-T8): invariants run in EndBlock; the dev
# collateral message is routed.
TEST_TAGS ?= invariants,devcredit

.PHONY: all build build-dev test lint proto proto-tools proto-lint check-go e2e e2e-M0 determinism-long localnet-start localnet-stop clean

all: build

# The SDK does not build for 32-bit targets; catch a wrong Go on PATH early.
check-go:
	@arch=$$($(GO) env GOARCH); if [ "$$arch" != "amd64" ] && [ "$$arch" != "arm64" ]; then \
		echo "error: $(GO) targets GOARCH=$$arch; use a 64-bit Go (see docs/status.md)"; exit 1; fi

build: check-go
	@mkdir -p $(BUILDDIR)
	$(GO) build -o $(BUILDDIR)/ ./cmd/keystoned

test: check-go
	GO=$(GO) bash scripts/gotest.sh -tags $(TEST_TAGS) ./...

lint: check-go
	$(GO) vet ./...
	$(GO) vet -tags $(TEST_TAGS) ./...
	$(GO) run ./tools/detlint/cmd/detlint ./...
	$(GO) run ./tools/detlint/cmd/detlint -tags=$(TEST_TAGS) ./...
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
		else echo "golangci-lint not installed; skipped"; fi

proto-tools:
	$(GO) install github.com/cosmos/gogoproto/protoc-gen-gocosmos@v1.7.2
	$(GO) install github.com/grpc-ecosystem/grpc-gateway/protoc-gen-grpc-gateway@v1.16.0

proto:
	bash scripts/protocgen.sh

proto-lint:
	cd proto && buf lint

# build-dev builds keystoned with the devcredit message (M1 demo; removed in M3) and mockfeed.
build-dev: check-go
	@mkdir -p $(BUILDDIR)
	$(GO) build -tags devcredit -o $(BUILDDIR)/ ./cmd/keystoned ./cmd/mockfeed

# e2e runs the demo of the latest finished milestone.
e2e: build-dev
	bash scripts/demo-M1.sh

e2e-M0: build
	bash scripts/demo-M0.sh

# determinism-long is M2 acceptance 1: 4 nodes, 10,000 heights (about 10 minutes).
determinism-long: check-go
	$(GO) test -tags long,devcredit -timeout 60m -run TestDeterminism10k -v ./testutil/multinode

localnet-start:
	@echo "localnet arrives in M2"; exit 1

localnet-stop:
	@echo "localnet arrives in M2"; exit 1

clean:
	rm -rf $(BUILDDIR)
