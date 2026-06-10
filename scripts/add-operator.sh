#!/usr/bin/env bash
# Adds a 5th operator to the running docker localnet (spec M2-T7):
#   1. `keystoned testnet add-node` writes localnet/homes/node4 (fresh keys, shared genesis);
#   2. node4 starts (compose profile "extra") and syncs;
#   3. the admin multisig (3 of 4 operator keys) signs MsgSetValidators adding it at power 1;
#      a version signed by only 2 of 4 is rejected first;
#   4. node4 must sign a block within 5 heights of joining the validator set.
# Needs: make localnet-start (running), make build (host keystoned), curl, jq.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=${BIN:-$ROOT/build/keystoned}
[ -x "$BIN" ] || BIN="$BIN.exe"
H=${LOCALNET_HOMES:-$ROOT/localnet/homes}
RPC=${RPC:-http://127.0.0.1:26657}
CHAIN=keystone-local-1
WORK=${WORK:-$ROOT/.demo/add-operator}
COMPOSE=(docker compose -f "$ROOT/localnet/docker-compose.yml")
KR=(--keyring-backend test --chain-id "$CHAIN")

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok:   $*"; }
height() { curl -fsS -m 3 "$RPC/status" | jq -r '.result.sync_info.latest_block_height'; }

[ -d "$H/node0" ] || fail "no localnet homes in $H (run make localnet-start)"
[ ! -e "$H/node4" ] || fail "$H/node4 already exists (make localnet-clean for a fresh localnet)"
rm -rf "$WORK"; mkdir -p "$WORK"

# 1. Node home.
peers=""
for j in 0 1 2 3; do peers+="$("$BIN" comet show-node-id --home "$H/node$j")@node$j:26656,"; done
"$BIN" testnet add-node --output "$H" --index 4 --docker --peers "${peers%,}" \
  --genesis "$H/node0/config/genesis.json" > "$WORK/node4.json" || fail "add-node"
OP=$(jq -r .operator_address "$WORK/node4.json")
PK=$(jq -r .consensus_pubkey "$WORK/node4.json")
CONS=$(jq -r .cons_address_hex "$WORK/node4.json")
pass "node4 home: operator $OP, consensus address $CONS"

# 2. Start and sync node4 before it joins.
"${COMPOSE[@]}" --profile extra up -d --no-deps node4 >/dev/null 2>&1 || fail "start node4"
for _ in $(seq 1 60); do
  s=$(curl -fsS -m 2 http://127.0.0.1:26697/status 2>/dev/null | jq -r '.result.sync_info.catching_up' 2>/dev/null || echo true)
  [ "$s" = "false" ] && break
  sleep 2
done
[ "$s" = "false" ] || fail "node4 did not sync"
pass "node4 synced at height $(curl -fsS http://127.0.0.1:26697/status | jq -r .result.sync_info.latest_block_height)"

# 3. MsgSetValidators from the admin multisig.
ADMIN=$("$BIN" keys show admin -a --home "$H/node0" --keyring-backend test)
entry=$(jq -nc --arg op "$OP" --arg pk "$PK" '{operator_address: $op, consensus_pubkey: $pk, moniker: "node4", power: "1"}')
"$BIN" tx poa set-validators "$entry" --from admin --generate-only --home "$H/node0" "${KR[@]}" --gas 2000000 \
  > "$WORK/unsigned.json" || fail "generate MsgSetValidators"
for k in 0 1 2; do
  "$BIN" tx sign "$WORK/unsigned.json" --multisig "$ADMIN" --from operator --sign-mode amino-json \
    --home "$H/node$k" "${KR[@]}" --output-document "$WORK/sig$k.json" || fail "operator $k signature"
done

"$BIN" tx multi-sign "$WORK/unsigned.json" admin "$WORK/sig0.json" "$WORK/sig1.json" \
  --home "$H/node0" "${KR[@]}" --output-document "$WORK/signed-2of4.json" || fail "assemble 2-of-4"
out=$("$BIN" tx broadcast "$WORK/signed-2of4.json" --home "$H/node0" -o json 2>&1 || true)
code=$(printf '%s' "$out" | jq -r '.code' 2>/dev/null || echo error)
[ "$code" != "0" ] || fail "a 2-of-4 multisig MsgSetValidators was accepted"
pass "2-of-4 multisig rejected (code $code)"

"$BIN" tx multi-sign "$WORK/unsigned.json" admin "$WORK/sig0.json" "$WORK/sig1.json" "$WORK/sig2.json" \
  --home "$H/node0" "${KR[@]}" --output-document "$WORK/signed-3of4.json" || fail "assemble 3-of-4"
out=$("$BIN" tx broadcast "$WORK/signed-3of4.json" --home "$H/node0" -o json)
[ "$(printf '%s' "$out" | jq -r .code)" = "0" ] || fail "3-of-4 broadcast: $out"
res=$("$BIN" q wait-tx "$(printf '%s' "$out" | jq -r .txhash)" --timeout 60s --home "$H/node0" -o json)
[ "$(printf '%s' "$res" | jq -r .code)" = "0" ] || fail "MsgSetValidators failed: $(printf '%s' "$res" | jq -r .raw_log)"
TXH=$(printf '%s' "$res" | jq -r .height)
pass "3-of-4 multisig MsgSetValidators included at height $TXH"

# 4. node4 joins at TXH + 2 and must sign a block within 5 heights of that.
JOIN=$((TXH + 2))
for _ in $(seq 1 60); do [ "$(height)" -ge $((JOIN + 6)) ] && break; sleep 2; done
n=$(curl -fsS "$RPC/validators?height=$JOIN" | jq -r '.result.total')
[ "$n" = "5" ] || fail "validator set at $JOIN has $n validators"
signed_at=
for ((hh = JOIN; hh <= JOIN + 5; hh++)); do
  s=$(curl -fsS "$RPC/block?height=$((hh + 1))" | jq -r --arg a "$CONS" \
    '[.result.block.last_commit.signatures[] | select(.validator_address == $a and .block_id_flag == 2)] | length')
  if [ "$s" = "1" ]; then signed_at=$hh; break; fi
done
[ -n "$signed_at" ] || fail "node4 did not sign within 5 heights of $JOIN"
pass "node4 in the validator set from height $JOIN; signed block $signed_at"
echo "add-operator passed"
