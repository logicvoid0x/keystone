SHELL := bash
GO ?= go
BUILDDIR ?= build
# make test builds with these tags (spec M1-T8): invariants run in EndBlock.
TEST_TAGS ?= invariants

.PHONY: all build build-dev test lint proto proto-tools proto-lint check-go zf-lib zf-lib-linux zf-lib-all zf-test e2e e2e-M0 e2e-M2 determinism-long localnet-bin localnet-start localnet-stop localnet-clean localnet-byz localnet-signer3 localnet-monitoring e2e-M3 e2e-M4a e2e-M4b upgrade-drill release release-verify clean

all: build

# FROST is the Zcash Foundation frost-secp256k1-tr crate behind a C ABI
# (crypto/frost/internal/zf), built as a static library per target and linked with
# cgo. zig cc is the C compiler and linker for every target, so cross builds need no
# other toolchain; cargo needs the Rust targets below (rustup target add ...).
ZIG ?= zig
CARGO ?= cargo
ZF := crypto/frost/internal/zf
ZF_SRC := $(ZF)/rust/Cargo.toml $(ZF)/rust/Cargo.lock $(wildcard $(ZF)/rust/src/*.rs)
ZF_TARGET_DIR := $(CURDIR)/$(BUILDDIR)/zf-target
RUST_TARGET_windows_amd64 := x86_64-pc-windows-gnu
RUST_TARGET_linux_amd64 := x86_64-unknown-linux-gnu
RUST_TARGET_linux_arm64 := aarch64-unknown-linux-gnu
# glibc 2.36 is the localnet image's (debian bookworm).
ZIG_TARGET_windows_amd64 := x86_64-windows-gnu
ZIG_TARGET_linux_amd64 := x86_64-linux-gnu.2.36
ZIG_TARGET_linux_arm64 := aarch64-linux-gnu.2.36
HOST := $(shell $(GO) env GOOS)_$(shell $(GO) env GOARCH)
export CGO_ENABLED := 1
export CC := $(ZIG) cc -target $(ZIG_TARGET_$(HOST))
# Paths are remapped so the library does not depend on where it was built.
ZF_RUSTFLAGS := --remap-path-prefix=$(CURDIR)=/keystone --remap-path-prefix=$(HOME)=/home

$(ZF)/lib/%/libkeystone_frost.a: $(ZF_SRC)
	cd $(ZF)/rust && CARGO_TARGET_DIR=$(ZF_TARGET_DIR) RUSTFLAGS="$(ZF_RUSTFLAGS)" \
		$(CARGO) build --release --locked --target $(RUST_TARGET_$*)
	@mkdir -p $(@D)
	cp $(ZF_TARGET_DIR)/$(RUST_TARGET_$*)/release/libkeystone_frost.a $@

zf-lib: $(ZF)/lib/$(HOST)/libkeystone_frost.a
zf-lib-linux: $(ZF)/lib/linux_amd64/libkeystone_frost.a
zf-lib-all: $(ZF)/lib/windows_amd64/libkeystone_frost.a $(ZF)/lib/linux_amd64/libkeystone_frost.a $(ZF)/lib/linux_arm64/libkeystone_frost.a

# zf-test runs the Rust crate's tests (the shim against the ZF entry points).
zf-test:
	cd $(ZF)/rust && CARGO_TARGET_DIR=$(ZF_TARGET_DIR) $(CARGO) test --locked

# The SDK does not build for 32-bit targets; catch a wrong Go on PATH early.
check-go:
	@arch=$$($(GO) env GOARCH); if [ "$$arch" != "amd64" ] && [ "$$arch" != "arm64" ]; then \
		echo "error: $(GO) targets GOARCH=$$arch; use a 64-bit Go (see docs/status.md)"; exit 1; fi
	@command -v $(ZIG) >/dev/null 2>&1 || { echo "error: zig not found (mise install, see mise.toml)"; exit 1; }

build: check-go zf-lib
	@mkdir -p $(BUILDDIR)
	$(GO) build -o $(BUILDDIR)/ ./cmd/keystoned

test: check-go zf-lib zf-test
	GO=$(GO) bash scripts/gotest.sh -tags $(TEST_TAGS) ./...
	GO=$(GO) bash scripts/gotest.sh -tags byzantine -run TestByzantineProposer ./testutil/multinode

lint: check-go zf-lib
	$(GO) vet ./...
	$(GO) vet -tags $(TEST_TAGS) ./...
	$(GO) vet -tags byzantine ./...
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

# build-dev builds keystoned and mockfeed (the demos run mockfeed).
build-dev: check-go zf-lib
	@mkdir -p $(BUILDDIR)
	$(GO) build -o $(BUILDDIR)/ ./cmd/keystoned ./cmd/mockfeed

# e2e runs the demo of the latest finished milestone (M5: needs Docker).
e2e: build-dev
	bash scripts/demo-M5.sh

e2e-M4b: build-dev
	bash scripts/demo-M4b.sh

# upgrade-drill rehearses the M6 x/upgrade drill on the localnet (design §17).
upgrade-drill: build-dev
	bash scripts/upgrade-drill.sh

e2e-M4a: build-dev
	bash scripts/demo-M4a.sh

e2e-M3: build-dev
	bash scripts/demo-M3.sh

e2e-M2: build-dev
	bash scripts/demo-M2.sh

e2e-M0: build
	bash scripts/demo-M0.sh

# determinism-long is M2 acceptance 1: 4 nodes, 10,000 heights (about 10 minutes).
determinism-long: check-go zf-lib
	$(GO) test -tags long -timeout 60m -run TestDeterminism10k -v ./testutil/multinode

# Docker localnet (spec M2-T1). Homes in $(LOCALNET_HOMES) are kept by localnet-stop.
LOCALNET_HOMES ?= localnet/homes
COMPOSE ?= docker compose -f localnet/docker-compose.yml

# localnet-bin cross-compiles the Linux binaries the localnet image copies.
LINUX_ENV := GOOS=linux GOARCH=amd64 CC="$(ZIG) cc -target $(ZIG_TARGET_linux_amd64)"
localnet-bin: check-go zf-lib-linux
	@mkdir -p $(BUILDDIR)/linux
	$(LINUX_ENV) $(GO) build -trimpath -o $(BUILDDIR)/linux/keystoned ./cmd/keystoned
	$(LINUX_ENV) $(GO) build -trimpath -tags byzantine -o $(BUILDDIR)/linux/keystoned-byz ./cmd/keystoned
	$(LINUX_ENV) $(GO) build -trimpath -tags keystone_pre_v2 -o $(BUILDDIR)/linux/keystoned-pre-v2 ./cmd/keystoned
	$(LINUX_ENV) $(GO) build -trimpath -o $(BUILDDIR)/linux/mockfeed ./cmd/mockfeed

# DEALER=--dev-dealer (default) puts a dealer key in genesis; `make localnet-start DEALER=`
# starts with no custody key and runs the epoch-1 DKG (M4b).
DEALER ?= --dev-dealer

localnet-start: build-dev localnet-bin
	@if [ ! -d $(LOCALNET_HOMES)/node0 ]; then \
		./$(BUILDDIR)/keystoned testnet init-files --operators 4 --docker $(DEALER) --output $(LOCALNET_HOMES) --chain-id keystone-local-1; fi
	$(COMPOSE) up -d --build mockfeed bitcoin0 bitcoin1 bitcoin2 bitcoin3 node0 node1 node2 node3

localnet-stop:
	$(COMPOSE) --profile extra --profile monitoring down

localnet-clean:
	$(COMPOSE) --profile extra --profile monitoring down -v
	rm -rf $(LOCALNET_HOMES)

# localnet-byz restarts node3 as a byzantine proposer: make localnet-byz MODE=extra_magic
# (drop_tx0, extra_magic, corrupt_one_ve, corrupt_two_ve; MODE= restores honest).
localnet-byz:
	KEYSTONE_BYZ=$(MODE) NODE3_BIN=$(if $(MODE),keystoned-byz,keystoned) $(COMPOSE) up -d --no-deps --force-recreate node3

# localnet-monitoring starts Prometheus (with the §16 alerts) and Grafana for the localnet.
localnet-monitoring:
	$(COMPOSE) --profile monitoring up -d prometheus grafana

# localnet-signer3 restarts node3 with its FROST signer on or off: make localnet-signer3 SIGNER=false
localnet-signer3:
	NODE3_SIGNER=$(if $(SIGNER),$(SIGNER),true) $(COMPOSE) up -d --no-deps --force-recreate node3

# release builds reproducible binaries and SHA256SUMS with the pinned Go (spec M6-T2);
# release-verify also rebuilds linux/amd64 and checks the hash matches.
release: zf-lib-all
	GO=$(GO) ZIG=$(ZIG) bash scripts/release.sh

release-verify: zf-lib-all
	GO=$(GO) ZIG=$(ZIG) bash scripts/release.sh --verify

clean:
	rm -rf $(BUILDDIR)
