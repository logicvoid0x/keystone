# M4a — FROST signing and regtest withdrawals

**Goal:** a withdrawal request becomes a deterministic Bitcoin transaction whose
inputs are signed by FROST, with nonce commitments and signature shares carried in
vote extensions and aggregated on-chain; the transaction confirms on regtest.
Keys still come from the dev dealer (M3).

**Design sections:** §9.6 (tweaks), §9.8, §9.9, §9.10 (selection, retry), §9.11, §13 A10–A13.

**Estimate:** 1–2 days. This is the riskiest milestone; keep commits small.

## Tasks

### T1 — `crypto/frost` (RFC 9591, secp256k1 Taproot variant)

- Implement the ciphersuite used by the Zcash Foundation `frost-secp256k1-tr` crate
  (see `docs/verified-apis.md`). Read its `src/lib.rs` and match it exactly:
  even-y normalization of the group key (`into_even_y`), TapTweak application,
  negation of nonces and commitment shares when the group commitment has odd y,
  binding-factor and challenge hashes.
- Use `btcec/v2` (`ModNScalar`, `JacobianPoint`) for all arithmetic. Secret-share operations
  are constant time; never use `math/big` for secrets.
- Split the package so on-chain code imports only public operations:
  - public (deterministic, used in `x/custody`): binding factors, group commitment,
    Lagrange coefficients, per-signer share verification, aggregation, BIP-340 verify;
  - secret (used only by `sidecar/signer`): nonce generation (RFC 9591 `nonce_generate`
    with `crypto/rand`), signature share, trusted-dealer keygen.
- Put the secret part in `crypto/frost/secret`, which `detlint` excludes; it is the only
  FROST code allowed to use `crypto/rand`, and only `sidecar/signer` may import it.
- Copy `vectors.json`, `vectors_dkg.json`, `vectors-big-identifier.json` from
  `frost-secp256k1-tr` and `frost-secp256k1` into `crypto/frost/testdata/` with a README
  stating the source commit. Every vector must pass byte for byte.
- Replace the M3 Shamir helper with `crypto/frost` trusted-dealer keygen.
- **Flag for audit** at the top of every file.

### T2 — Nonce store (`sidecar/signer/noncestore`, design §9.9)

- bbolt file `<home>/custody/nonces.db`, fsync on every write, permissions 0600.
- Record per (epoch, share, seq): secret nonces, public commitments, `used_for` session id.
- `Generate` persists before returning commitments. `Bind(share, seq, sessionID)` sets
  `used_for` atomically; binding to a different session fails and raises an alarm;
  re-binding to the same session returns the same nonces.
- Startup check: a missing or corrupt DB sets `revoke_all_nonces` in the next VE and
  generates a fresh pool. Never restore this file from backup.

### T3 — Nonce pools on-chain

- `FrostPayload.commitments`: each signer tops its pool up to `NoncePool` (8) unconsumed
  commitments, at most 2 new per block.
- `x/custody` records commitments by (epoch, share, seq), accepting seq only above the last
  recorded; `revoke_all_nonces` discards that operator's unconsumed commitments.

### T4 — Withdrawal queue and deterministic transactions (§9.8 steps 1–3)

- `MsgWithdrawCollateral` and `MsgWithdrawClaim` debit immediately and append a
  `WithdrawalRequest` (P2TR, P2WPKH or P2WSH only; ≥ `MinWithdrawSat`).
- `BuildOps` emits `OpenBatch` when none is in flight and the queue has ≥ 16 requests or its
  oldest is 20 blocks old.
- Construction exactly per §9.8: median VE feerate clamped to [1, 200]; vsize formula;
  per-request fee share, remainder to fee reserve; coin selection by (amount desc, txid asc,
  vout asc), ≤ 8 inputs; outputs in request id order then change to Q0 if ≥ 330 sat;
  version 2, locktime 0, nSequence 0xFFFFFFFD.
- Sighash per input with `txscript.CalcTaprootSignatureHash(sigHashes, SigHashDefault, tx, idx, fetcher)`
  (signature in `docs/verified-apis.md`); `NewMultiPrevOutFetcher` used only for lookups.
- One `SigningSession` per input; inputs `LOCKED`.
- Golden tests: a fixed state produces byte-identical unsigned transactions across runs.

### T5 — Sessions and aggregation (§9.10, §9.11)

- Signer selection per §9.10: active epoch operators, not blamed, VE in the previous block,
  unconsumed commitment; sort by (missed asc, operator address asc); take t.
- Session id = SHA-256(chain_id ‖ epoch ‖ txid ‖ input_index ‖ attempt).
- `BuildOps` processes every OPEN session in id order exactly as the §9.11 on-chain
  pseudocode: verify each operator's share with public FROST verification; an invalid share
  fails the session with blame; all valid → aggregate, BIP-340 verify against the tweaked
  output key (panic only on an aggregation bug), `SessionSigned`; deadline → `SessionFailed`
  with the missing operators; retries open with attempt + 1 and fresh assignments, or `STALLED`.
- When every input is signed: assemble witnesses, store the signed tx, emit `withdrawal_signed`.
- Custody op order exactly as listed at the end of §9.11.

### T6 — Signer sidecar (`sidecar/signer`)

`FrostSigner.Payload` exactly as the §9.11 signer-side code: cached shares per session,
`Bind` before computing, replenish commitments, canonical sort. Loads the share from
`<home>/custody/epoch_<n>/shares.json` (Argon2id + AES-256-GCM, passphrase from env or file).

### T7 — Broadcast, confirmation, RBF

- The watcher submits every `withdrawal_signed` tx to its bitcoind and resubmits every 10
  Keystone blocks until confirmed.
- `TxConfirmed` attestation at depth k with > 2/3 power → inputs spent, change `AVAILABLE`,
  requests `COMPLETED`, event `tx_confirmed`.
- RBF per §9.8 step 7 after 6 anchor blocks without confirmation.

### T8 — Tests

- `crypto/frost` vectors; random 3-of-4 sign/verify property test; tweak cases with odd-y keys.
- Nonce store crash test: kill the signer process between `Bind` and VE send at every step;
  restart; assert the same share is re-sent and no nonce is ever bound to two sessions.
- Harness (M2) extended with signer sidecars: a 4-node run signs 50 withdrawals with
  zero app-hash divergence; one node sending an invalid share is blamed and the retry signs.

## Acceptance checks

1. `make lint test` passes; FROST vectors pass byte for byte.
2. `scripts/demo-M4a.sh` on the localnet:
   1. deposit 1 BTC to a vault (M3 flow), mint, then `MsgWithdrawCollateral` 0.2 BTC to a regtest address;
   2. within about 25 Keystone blocks the batch opens and `withdrawal_signed` appears;
   3. the tx is in the regtest mempool; mine 1 block; `tx_confirmed` follows; the recipient
      wallet shows 0.2 BTC minus its fee share;
   4. with node3's signer stopped, another withdrawal still signs (3 of 4 = t).
