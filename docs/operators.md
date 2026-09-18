# Running a Keystone operator (signet testnet)

This guide follows design §16. An operator runs one `keystoned` (consensus node, price
fetcher, Bitcoin watcher, FROST signer and DKG participant in one process) next to its
own `bitcoind`.

## 1. Hardware and software

| Item | Minimum |
| --- | --- |
| CPU / RAM | 4 cores, 8 GB |
| Disk | 100 GB SSD for `keystoned`; signet bitcoind about 10 GB (pruning allowed) |
| Network | Static address or DNS name; ports 26656 (p2p) reachable from the other operators |
| OS | Linux amd64/arm64 or macOS arm64 |
| Bitcoin | Bitcoin Core 28 or later on **signet**, `-txindex` not needed |

Use only release binaries (`make release` builds them reproducibly; compare your
`build/release/SHA256SUMS` with the published one, or rebuild with `make release-verify`).
`keystoned version --long` prints the version, commit and Go version. Never run binaries
built with the `byzantine` or `invariants` tags.

## 2. bitcoind

```
bitcoind -signet -server -prune=5000 -rpcuser=keystone -rpcpassword=<secret> \
  -zmqpubhashblock=tcp://127.0.0.1:28333
```

In `app.toml`:

```toml
[keystone.bitcoin]
enabled = true
network = "signet"
rpc-url = "http://127.0.0.1:38332"
rpc-user = "keystone"
rpc-password = "<secret>"
zmq-block = "tcp://127.0.0.1:28333"
fixed-feerate = 0          # use estimatesmartfee
scan-window = 1000
```

The chain's custody `bitcoin_network` is `signet` and its confirmation depth is 6.

## 3. Price sources

Configure at least three real exchanges in `[keystone.price]` (design §6.2). Each source
must be independent (different venue, ideally different region). The node's VE carries
the local median only when at least `MinSources` (3) answered within the ExtendVote
deadline; check `keystone_oracle_local_sources` after startup.

## 4. Keys

```
keystoned init <moniker> --chain-id keystone-signet-1           # consensus key
keystoned keys add operator --keyring-backend file              # account key (admin multisig member)
export KEYSTONE_CUSTODY_PASSPHRASE=...                           # or --passphrase-file
keystoned genesis operator-info <moniker> --keyring-backend file # creates custody keys, prints JSON
```

`operator-info` creates `custody/enc_key.json` (Argon2id + AES-256-GCM) and the custody
hot key `custody` in the keyring, and prints the public keys the genesis ceremony needs.
Send that JSON to the ceremony coordinator. After genesis, set in `app.toml`:

```toml
[keystone.signer]
enabled = true
passphrase-file = "custody/passphrase"   # or KEYSTONE_CUSTODY_PASSPHRASE
operator = "<your operator address>"
```

The DKG for epoch 1 runs on-chain from height 2 (about 60 blocks). When
`epoch_activated` appears, run `keystoned custody shares 1` and check that its
`group_key` equals the event's: that is the key ceremony check of design §16.

Operators added after genesis run the same commands, send their consensus pubkey to the
admin signers, and submit `keystoned tx custody register-custody-key <enc_pubkey> <hot_key>`.

## 5. Backups

| File | Back up? |
| --- | --- |
| `config/priv_validator_key.json` | Yes (offline, encrypted) |
| Keyring (`operator`, `custody`) | Yes |
| `custody/enc_key.json` | Yes (it is encrypted) |
| `custody/epoch_<n>/shares.json` | Yes, once per epoch, right after `epoch_activated` |
| `custody/nonces.db` | **Never.** Never restore it. |

Restoring after a loss: restore shares and keys only, delete any `custody/nonces.db`, and
start with `keystoned start --revoke-nonces` (design §9.5). The signer also revokes on its
own when it finds commitments on chain that its nonce store lacks. A `FROST SIGNER ALARM`
in the logs (metric `keystone_nonce_alarms_total`) means a nonce was asked for a second
session: treat it as a possible compromise and tell the other operators.

## 6. Health and monitoring

Prometheus metrics are on `:26660` (`instrumentation.prometheus = true`). The alerts of
design §16 are in `localnet/monitoring/alerts.yml` and a dashboard in
`localnet/monitoring/grafana/dashboards/keystone.json`; load both into your own
Prometheus/Grafana. A healthy node shows:

- `keystone_oracle_status` 1 and `keystone_oracle_local_price` > 0;
- `keystone_bitcoind_tip_age_seconds` well under 1,800 (signet blocks every ~10 min);
- `keystone_nonce_pool_level` near 8 for your share;
- `keystone_sessions_stalled` 0 and `keystone_deficit_sat` 0.

## 7. Public endpoints

Only one operator exposes RPC/gRPC publicly, behind a rate limiter;
`localnet/public/nginx.conf` is an example (100 requests per second per client, bursts
of 200). Validators keep their own RPC on localhost.

## 8. Upgrades

Upgrades use `x/upgrade` plans signed by the admin multisig (design §17). At the plan
height every node stops with `UPGRADE "<name>" NEEDED`; replace the binary with the
published release and start again. Do not restore from a snapshot around an upgrade:
the nonce store must never roll back.
