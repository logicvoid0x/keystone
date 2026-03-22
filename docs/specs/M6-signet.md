# M6 — Signet testnet

**Goal:** 7 independent operators run Keystone against Bitcoin signet with the full
flow public: deposit, mint, liquidation, repay, withdrawal.

**Design sections:** §1 (success criteria), §9.1 (testnet column), §14, §16, §17.

**Estimate:** 1 day of engineering plus operator coordination.

## Tasks

1. **Network config:** signet in `[keystone.bitcoin]`; k = 6; testnet parameter set from
   design §9.1 and §10.3 (`MembershipDelayBlocks` 28,800, `TreasuryCapSat` 10 BTC).
2. **Release build:** reproducible builds (pinned Go toolchain, `-trimpath`, checksums);
   `byzantine`, `invariants` and dev tags excluded; version command prints commit and Go version.
3. **Operator onboarding guide** (`docs/operators.md`) following design §16: hardware,
   bitcoind (pruning allowed), price sources (at least 3 real exchanges), custody keys,
   backup rules (shares yes, nonce DB never), health endpoint, alert setup.
4. **Genesis ceremony script:** collects each operator's consensus pubkey, custody encryption
   pubkey and hot-key address; builds genesis with the admin multisig (5 of 7); publishes the
   genesis hash. DKG runs on-chain from height 2.
5. **Public endpoints:** one RPC/gRPC node behind a rate limiter. A faucet is out of scope unless asked.
6. **Upgrade drill:** an `x/upgrade` plan executed on testnet with a VE version bump per §17.

## Acceptance checks

1. 7 operators live; 99% of blocks commit a fresh price over 24 hours (success criterion 2).
2. The end-to-end demo from design §1 (deposit → mint → price drop → liquidation → repay →
   withdrawal) runs on signet and every step is visible in a block explorer and events.
3. The upgrade drill completes with no halt beyond the planned upgrade height.
