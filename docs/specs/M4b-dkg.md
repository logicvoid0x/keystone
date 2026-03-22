# M4b — Distributed key generation

**Goal:** a fresh localnet creates its FROST group key with no dealer, through
transactions, and then signs withdrawals exactly as in M4a.

**Design sections:** §4.4 (custody keys), §9.3, §10.1 (`0x13`, `0x14`, `0x18`), §11, §13 A9–A10.

**Estimate:** 1 day.

## Tasks

### T1 — Custody keys and authz

- `keystoned custody init`: creates the secp256k1 custody encryption key (encrypted at rest,
  Argon2id + AES-256-GCM) and a custody hot key in the keyring.
- `MsgRegisterCustodyKey{pubkey}` (operator-signed) for operators added after genesis;
  genesis carries each initial operator's encryption pubkey and hot-key address (§10.4).
- Each operator grants its hot key authz for `MsgDKGCommit`, `MsgDKGDeal`, `MsgDKGComplaint`.
  Add `x/authz` to the app for this, or accept the hot key directly in custody state;
  pick one and record it in `docs/decisions.md`.

### T2 — DKG messages and state machine (§9.3)

Phases with 20-block windows: Commit → Deal → Complain → Finalize.

- `MsgDKGCommit{epoch, coeff_commitments[t], pok}`: proof of knowledge of a₀ bound to
  (chain id, epoch, dealer). Reject bad proofs on submission.
- `MsgDKGDeal{epoch, ciphertexts[]}`: one ciphertext per recipient; ECDH to the recipient's
  encryption key, HKDF-SHA256, AES-256-GCM with associated data (epoch, dealer, recipient).
- `MsgDKGComplaint{dealer, ecdh_secret, dleq_proof}`: the chain decrypts, checks
  f_v(i)·G against the commitments, and disqualifies the dealer if the complaint holds,
  otherwise the complainer.
- Finalize in EndBlock: group key, aggregated commitments, verifying shares for all operators;
  if fewer than 2/3 of operators qualify, restart once, then alert.
- The signer sidecar watches DKG state and submits its messages with the hot key; it derives
  its share, checks it against its verifying share, writes `epoch_<n>/shares.json`.
- Use `crypto/frost` for polynomial evaluation and verification; check against
  `vectors_dkg.json`.

### T3 — Genesis without a dealer

`testnet init-files` without `--dev-dealer` leaves custody with no epoch; the epoch-1 DKG starts
at height 2; deposits are rejected until it is `ACTIVE`.

### T4 — Tests

- DKG vectors; a full 4-operator DKG in the harness; one dealer sends a bad share → valid
  complaint disqualifies it and the key still forms from 3 dealers; a false complaint
  disqualifies the complainer.

## Acceptance checks

1. `make lint test` passes; DKG vectors pass.
2. `scripts/demo-M4b.sh`: fresh localnet without `--dev-dealer`; epoch 1 becomes `ACTIVE`
   within about 80 blocks; every node's derived group key matches `epoch_activated`;
   the M4a deposit-and-withdraw demo then succeeds.
