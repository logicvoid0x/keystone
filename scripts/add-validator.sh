#!/usr/bin/env bash
# Adds a 5th validator to the running docker localnet (spec M2-T7, M7-T7):
#   1. `keystoned testnet add-node` writes localnet/homes/node4 (fresh keys, shared genesis);
#   2. node4 starts (compose profile "extra") and syncs;
#   3. node4's operator is funded with ukey from the node0/node1 test accounts;
#   4. node4's MsgCreateValidator is rejected while it is not allowlisted;
#   5. the admin multisig allowlists it (MsgSetAllowlist); a 2-of-4 signature is
#      rejected first, 3 of 4 is accepted;
#   6. node4 sends MsgCreateValidator bonding BOND ukey and must sign a block within 5
#      heights of joining the validator set.
# Needs: make localnet-start (running), make build (host keystoned), curl, jq.
# BOND (default 500000000000 ukey, power 500,000) sets node4's self-bond.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=${BIN:-$ROOT/build/keystoned}
[ -x "$BIN" ] || BIN="$BIN.exe"
H=${LOCALNET_HOMES:-$ROOT/localnet/homes}
RPC=${RPC:-http://127.0.0.1:26657}
CHAIN=keystone-local-1
WORK=${WORK:-$ROOT/.demo/add-validator}
BOND=${BOND:-500000000000}
COMPOSE=(docker compose -f "$ROOT/localnet/docker-compose.yml")
KR=(--keyring-backend test --chain-id "$CHAIN")

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok:   $*"; }
height() { curl -fsS -m 3 "$RPC/status" | jq -r '.result.sync_info.latest_block_height' | tr -d '\r'; }
# wait_tx prints the result of a broadcast tx (json) once included.
wait_tx() {
  local out=$1 code
  code=$(printf '%s' "$out" | jq -r .code | tr -d '\r')
  [ "$code" = "0" ] || { printf '%s' "$out"; return 1; }
  "$BIN" q wait-tx "$(printf '%s' "$out" | jq -r .txhash | tr -d '\r')" --timeout 60s --home "$H/node0" -o json
}

[ -d "$H/node0" ] || fail "no localnet homes in $H (run make localnet-start)"
[ ! -e "$H/node4" ] || fail "$H/node4 already exists (make localnet-clean for a fresh localnet)"
rm -rf "$WORK"; mkdir -p "$WORK"

# 1. Node home.
peers=""
for j in 0 1 2 3; do peers+="$("$BIN" comet show-node-id --home "$H/node$j" | tr -d '\r')@node$j:26656,"; done
"$BIN" testnet add-node --output "$H" --index 4 --docker --peers "${peers%,}" \
  --genesis "$H/node0/config/genesis.json" > "$WORK/node4.json" || fail "add-node"
OP=$(jq -r .operator_address "$WORK/node4.json" | tr -d '\r')
VAL=$(jq -r .validator_address "$WORK/node4.json" | tr -d '\r')
PK=$(jq -r .consensus_pubkey "$WORK/node4.json" | tr -d '\r')
CONS=$(jq -r .cons_address_hex "$WORK/node4.json" | tr -d '\r')
pass "node4 home: operator $OP ($VAL), consensus address $CONS"

# 2. Start and sync node4 before it joins.
"${COMPOSE[@]}" --profile extra up -d --no-deps bitcoin4 node4 >/dev/null 2>&1 || fail "start node4"
s=true
for _ in $(seq 1 60); do
  s=$(curl -fsS -m 2 http://127.0.0.1:26697/status 2>/dev/null | jq -r '.result.sync_info.catching_up' 2>/dev/null | tr -d '\r' || echo true)
  [ "$s" = "false" ] && break
  sleep 2
done
[ "$s" = "false" ] || fail "node4 did not sync"
pass "node4 synced"

# 3. Fund node4's operator: half of BOND (plus 1 ukey) from each of two test accounts.
half=$(( BOND / 2 + 1 ))
for j in 0 1; do
  out=$("$BIN" tx bank send test "$OP" "${half}ukey" --home "$H/node$j" "${KR[@]}" --gas 200000 -y -o json)
  wait_tx "$out" >/dev/null || fail "fund from node$j: $out"
done
pass "node4 operator funded with $((2 * half)) ukey"

# 4. create-validator before allowlisting: rejected by the ante handler.
cat > "$WORK/validator.json" <<EOF
{
  "pubkey": {"@type": "/cosmos.crypto.ed25519.PubKey", "key": "$PK"},
  "amount": "${BOND}ukey",
  "moniker": "node4",
  "commission-rate": "0.05",
  "commission-max-rate": "0.20",
  "commission-max-change-rate": "0.01",
  "min-self-delegation": "1"
}
EOF
cp "$WORK/validator.json" "$H/node4/validator.json"
create() { "$BIN" tx staking create-validator "$H/node4/validator.json" --from operator --home "$H/node4" "${KR[@]}" --gas 400000 -y -o json --node "$RPC"; }
out=$(create 2>&1 || true)
code=$(printf '%s' "$out" | jq -r '.code' 2>/dev/null | tr -d '\r' || echo error)
[ "$code" != "0" ] || fail "create-validator accepted before allowlisting"
pass "create-validator rejected before allowlisting (code $code)"

# 5. MsgSetAllowlist from the admin multisig.
ADMIN=$("$BIN" keys show admin -a --home "$H/node0" --keyring-backend test | tr -d '\r')
"$BIN" tx admin set-allowlist --add "$(jq -nc --arg op "$OP" '{operator: $op, moniker: "node4"}')" \
  --from admin --generate-only --home "$H/node0" "${KR[@]}" --gas 2000000 > "$WORK/unsigned.json" || fail "generate MsgSetAllowlist"
for k in 0 1 2; do
  "$BIN" tx sign "$WORK/unsigned.json" --multisig "$ADMIN" --from operator --sign-mode amino-json \
    --home "$H/node$k" "${KR[@]}" --output-document "$WORK/sig$k.json" || fail "operator $k signature"
done
"$BIN" tx multi-sign "$WORK/unsigned.json" admin "$WORK/sig0.json" "$WORK/sig1.json" \
  --home "$H/node0" "${KR[@]}" --output-document "$WORK/signed-2of4.json" || fail "assemble 2-of-4"
out=$("$BIN" tx broadcast "$WORK/signed-2of4.json" --home "$H/node0" -o json 2>&1 || true)
code=$(printf '%s' "$out" | jq -r '.code' 2>/dev/null | tr -d '\r' || echo error)
[ "$code" != "0" ] || fail "a 2-of-4 multisig MsgSetAllowlist was accepted"
pass "2-of-4 multisig rejected (code $code)"
"$BIN" tx multi-sign "$WORK/unsigned.json" admin "$WORK/sig0.json" "$WORK/sig1.json" "$WORK/sig2.json" \
  --home "$H/node0" "${KR[@]}" --output-document "$WORK/signed-3of4.json" || fail "assemble 3-of-4"
out=$("$BIN" tx broadcast "$WORK/signed-3of4.json" --home "$H/node0" -o json)
res=$(wait_tx "$out") || fail "MsgSetAllowlist: $out"
[ "$(printf '%s' "$res" | jq -r .code | tr -d '\r')" = "0" ] || fail "MsgSetAllowlist failed: $(printf '%s' "$res" | jq -r .raw_log)"
pass "3-of-4 multisig MsgSetAllowlist included at height $(printf '%s' "$res" | jq -r .height | tr -d '\r')"

# 6. create-validator, then node4 joins two heights later and must sign within 5.
out=$(create)
res=$(wait_tx "$out") || fail "create-validator: $out"
[ "$(printf '%s' "$res" | jq -r .code | tr -d '\r')" = "0" ] || fail "create-validator failed: $(printf '%s' "$res" | jq -r .raw_log)"
TXH=$(printf '%s' "$res" | jq -r .height | tr -d '\r')
pass "MsgCreateValidator included at height $TXH"
JOIN=$((TXH + 2))
for _ in $(seq 1 60); do [ "$(height)" -ge $((JOIN + 6)) ] && break; sleep 2; done
n=$(curl -fsS "$RPC/validators?height=$JOIN" | jq -r '.result.total' | tr -d '\r')
[ "$n" = "5" ] || fail "validator set at $JOIN has $n validators"
signed_at=
for ((hh = JOIN; hh <= JOIN + 5; hh++)); do
  s=$(curl -fsS "$RPC/block?height=$((hh + 1))" | jq -r --arg a "$CONS" \
    '[.result.block.last_commit.signatures[] | select(.validator_address == $a and .block_id_flag == 2)] | length' | tr -d '\r')
  if [ "$s" = "1" ]; then signed_at=$hh; break; fi
done
[ -n "$signed_at" ] || fail "node4 did not sign within 5 heights of $JOIN"
pass "node4 in the validator set from height $JOIN; signed block $signed_at"
echo "add-validator passed"
