#!/usr/bin/env bash
# M0 demo (docs/specs/M0-scaffold.md, acceptance check 2):
#   1. one-node home via `testnet init-files --operators 1`, start, wait for height 20;
#   2. block 1 has no tx starting with KSTN;
#   3. heights 2..20: txs[0] decodes as tx0 and no other tx has the magic;
#   4. `keystoned q poa validators` lists 1 operator at power 1;
#   5. stop the node.
# Needs: a built binary (make build), curl, jq, base64.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=${BIN:-$ROOT/build/keystoned}
[ -x "$BIN" ] || BIN="$BIN.exe"
WORK=${WORK:-$ROOT/.demo/M0}
HOME0="$WORK/node0"
RPC=${RPC:-http://127.0.0.1:26657}
TARGET=20
TIMEOUT=${TIMEOUT:-240}

for tool in curl jq base64; do
  command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done
[ -x "$BIN" ] || { echo "binary not found: $BIN (run make build)" >&2; exit 1; }

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok:   $*"; }

rm -rf "$WORK"
"$BIN" testnet init-files --operators 1 --output "$WORK" --chain-id keystone-local-1 >/dev/null 2>&1 \
  || fail "testnet init-files"
pass "init-files wrote $HOME0"

"$BIN" start --home "$HOME0" >"$WORK/node.log" 2>&1 &
PID=$!
cleanup() {
  if kill -0 "$PID" 2>/dev/null; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

height() { curl -fsS "$RPC/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height' 2>/dev/null || echo 0; }

start=$SECONDS
while :; do
  h=$(height)
  [[ "$h" =~ ^[0-9]+$ ]] || h=0
  [ "$h" -ge "$TARGET" ] && break
  kill -0 "$PID" 2>/dev/null || { tail -40 "$WORK/node.log" >&2; fail "node exited"; }
  [ $((SECONDS - start)) -lt "$TIMEOUT" ] || { tail -40 "$WORK/node.log" >&2; fail "height $h after ${TIMEOUT}s"; }
  sleep 2
done
pass "reached height $h in $((SECONDS - start))s"

# txs of a block as base64 lines
block_txs() { curl -fsS "$RPC/block?height=$1" | jq -r '.result.block.data.txs // [] | .[]'; }
has_magic() { [ "$(printf '%s' "$1" | base64 -d 2>/dev/null | head -c 4)" = "KSTN" ]; }

while read -r tx; do
  [ -z "$tx" ] && continue
  has_magic "$tx" && fail "block 1 has a KSTN tx"
done < <(block_txs 1)
pass "block 1 has no tx0"

for ((hh = 2; hh <= TARGET; hh++)); do
  mapfile -t txs < <(block_txs "$hh")
  [ "${#txs[@]}" -ge 1 ] || fail "block $hh has no txs"
  has_magic "${txs[0]}" || fail "block $hh: txs[0] is not tx0"
  decoded=$("$BIN" debug decode-tx0 "${txs[0]}" 2>&1) || fail "block $hh: txs[0] does not decode: $decoded"
  votes=$(printf '%s' "$decoded" | jq '.votes | length')
  [ "$votes" -eq 1 ] || fail "block $hh: tx0 has $votes votes, want 1"
  for ((i = 1; i < ${#txs[@]}; i++)); do
    has_magic "${txs[$i]}" && fail "block $hh: tx $i has the magic"
  done
done
pass "blocks 2..$TARGET: txs[0] decodes as tx0, no other magic tx"

vals=$("$BIN" q poa validators --home "$HOME0" -o json)
n=$(printf '%s' "$vals" | jq '.validators | length')
p=$(printf '%s' "$vals" | jq -r '.validators[0].power | tostring')
[ "$n" -eq 1 ] && [ "$p" = "1" ] || fail "poa validators: $vals"
pass "q poa validators: 1 operator at power 1"

cleanup
trap - EXIT
pass "node stopped"
echo "M0 demo passed"
