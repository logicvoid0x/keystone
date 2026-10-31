# M7a: FROST through the Zcash Foundation crate

Inserted by the user between M7-T7 and M7-T8 (2026-10-27): FROST operations use the
Zcash Foundation `frost-secp256k1-tr` crate, linked into keystoned with cgo, instead of
Keystone's own Go port of it.

## Scope

- No wire, VE, tx0, state or store-layout change. The hand-written Go code matched the
  crate byte for byte, so outputs are identical; the crate's vectors prove it.
- The `crypto/frost` and `crypto/frost/secret` Go APIs keep their shape. The FROST math
  behind them moves to the crate. Keystone's DLEQ / share-encryption code and the
  btcec group helpers stay in Go (they are not FROST).

## Tasks

| Task | What | Files → tests |
| --- | --- | --- |
| T1 | Rust shim `keystone-frost` (C ABI, panics caught) over frost-core / frost-secp256k1-tr 3.0.0 with `internals`; tweak by stored scalar; context-bound proof of knowledge; combined-share check | `crypto/frost/internal/zf/rust` → `cargo test` (equality with `sign_with_tweak` / `aggregate_with_tweak` / `compute_proof_of_knowledge`) |
| T2 | cgo package `crypto/frost/internal/zf`; `crypto/frost` and `crypto/frost/secret` call it | `crypto/frost/...` → vector, property, DKG tests; zf panic / invalid-input tests |
| T3 | Build: zig cc for cgo on every target, `make zf-lib` / `zf-lib-linux` / `zf-lib-all` / `zf-test`, localnet cross build, release script | `Makefile`, `scripts/release.sh`, `mise.toml` → `make lint test`, `make localnet-bin` |
| T4 | Docs: CLAUDE.md, design §9.6 / §13 / §15, verified APIs, decisions, status | — |

## Acceptance

1. `make lint test` passes (including `cargo test` of the shim).
2. The ZF signing and DKG vectors pass through the linked crate.
3. `make localnet-bin` cross-builds the Linux binaries, and they run in the localnet
   image (debian bookworm).
4. An existing regtest demo that signs (M4a or M5) passes on the localnet.
