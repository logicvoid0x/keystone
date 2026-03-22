# M5 — Membership changes and robustness

**Goal:** operator set changes move custody to a fresh key safely; signing survives
faulty operators; fees bump; UTXOs consolidate; every failure in design §12 that the
code can detect is handled as specified.

**Design sections:** §3.2 (membership delay), §9.4, §9.5, §9.8 steps 7–8, §9.10, §12, §16.

**Estimate:** 1–2 days.

## Tasks

### T1 — Removal guard (§9.4)

`x/custody` implements the `x/poa` `RemovalGuard` (from M0): reject any `MsgSetValidators`
that would leave fewer than t operators of a funded epoch active. Wire it at app level.

### T2 — Membership delay and new epochs (§9.4)

- Track the active validator set hash; when it differs from the custody set and stays
  unchanged for `MembershipDelayBlocks`, start a DKG for epoch e+1 (M4b machinery);
  epoch e becomes `RETIRING`. Emit `custody_membership_pending` while waiting.
- Sweep: batches (kind `SWEEP`) of up to 8 epoch-e UTXOs to epoch e+1's Q0, signed by
  active epoch-e operators.
- Epoch-e deposit addresses stay watched for 1,000 blocks; new addresses per vault for e+1.
- `RETIRED` once every epoch-e UTXO is spent at depth k; signers delete that epoch's shares.
- `EmergencyRekey` param: when only t epoch operators have been online for 100 blocks, the
  admin can trigger an immediate new epoch among the online set.

### T3 — Retry-and-blame hardening (§9.10)

Blame for the epoch on invalid shares; per-retry exclusion on missing shares; `STALLED`
handling; `signer_flagged` after 3 invalid shares or 20% missed sessions.

### T4 — RBF and consolidation (§9.8 steps 7–8)

RBF at ×1.5 feerate (and at least the BIP-125 minimum increase) after 6 anchor blocks;
extra fee from change, topped up from the fee reserve. Consolidation of the 8 smallest
UTXOs when more than 50 are available and feerate ≤ 5 sat/vB.

### T5 — Failure-mode coverage

For each row of design §12 that has an automatic response, add a harness or localnet test
showing that response. List any row that cannot be tested automatically in
`docs/decisions.md` with the manual check instead.

### T6 — Observability (§16)

All metrics and alerts in §16, plus a Prometheus + Grafana compose profile with one dashboard.

## Acceptance checks

1. `make lint test` passes; the M2 determinism run passes with membership changes in the workload.
2. `scripts/demo-M5.sh`:
   1. with funds in custody, the admin removes node3 (power 0); `custody_membership_pending`
      appears; after the delay a new epoch forms among the remaining operators and all UTXOs
      sweep to it; the old epoch reaches `RETIRED`;
   2. an attempt to remove 2 of 4 operators at once is rejected by the guard;
   3. kill a signer mid-session; the session retries and signs;
   4. a withdrawal broadcast with a too-low feerate is replaced via RBF and confirms.
