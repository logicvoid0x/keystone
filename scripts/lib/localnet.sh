# Shared helpers for the docker localnet demos (sourced by scripts/demo-M2.sh and
# scripts/demo-M3.sh). Expects ROOT (repository root) and WORK (scratch dir) to be set.
# Needs: Docker, curl, jq.

BIN=${BIN:-$ROOT/build/keystoned}
[ -x "$BIN" ] || BIN="$BIN.exe"
H=$ROOT/localnet/homes
CHAIN=keystone-local-1
FEED=http://127.0.0.1:18080
RPCS=(http://127.0.0.1:26657 http://127.0.0.1:26667 http://127.0.0.1:26677 http://127.0.0.1:26687)
PROMS=(http://127.0.0.1:26660 http://127.0.0.1:26670 http://127.0.0.1:26680 http://127.0.0.1:26690)
KR=(--keyring-backend test --chain-id "$CHAIN")

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok:   $*"; }
height() { curl -fsS -m 3 "${1:-${RPCS[0]}}/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height' 2>/dev/null || echo 0; }
wait_height() { # $1 target, $2 timeout seconds
  local start=$SECONDS h
  while :; do
    h=$(height); [[ "$h" =~ ^[0-9]+$ ]] || h=0
    [ "$h" -ge "$1" ] && return 0
    [ $((SECONDS - start)) -lt "${2:-300}" ] || fail "timeout waiting for height $1 (at $h)"
    sleep 2
  done
}
q() { "$BIN" q "$@" --home "$H/node0" -o json; }
price_field() { q oracle price | jq -r ".price.$1"; }
feed() { curl -fsS -X POST "$FEED/admin/$1" -H 'Content-Type: application/json' -d "$2" >/dev/null; }
collateral() { q vault vault "$1" | jq -r .vault.vault.collateral_sat; }

# wait_tx <broadcast-json>: wait for inclusion and require code 0.
wait_tx() {
  local out=$1 hash res
  [ "$(printf '%s' "$out" | jq -r .code)" = "0" ] || fail "CheckTx: $out"
  hash=$(printf '%s' "$out" | jq -r .txhash)
  res=$("$BIN" q wait-tx "$hash" --timeout 60s --home "$H/node0" -o json) || fail "not included: $hash"
  [ "$(printf '%s' "$res" | jq -r .code)" = "0" ] || fail "tx failed: $(printf '%s' "$res" | jq -r .raw_log)"
}

# utx <node> <key> <module> <cmd> [args...]: user tx from node<N>'s keyring.
utx() {
  local n=$1 key=$2; shift 2
  wait_tx "$("$BIN" tx "$@" --from "$key" --home "$H/node$n" "${KR[@]}" --gas 2000000 --yes -o json)"
}

# atx <module> <cmd> [args...]: admin tx, signed by the 3-of-4 operator multisig.
atx() {
  local admin; admin=$("$BIN" keys show admin -a --home "$H/node0" --keyring-backend test)
  "$BIN" tx "$@" --from admin --generate-only --home "$H/node0" "${KR[@]}" --gas 2000000 > "$WORK/a.json"
  for k in 0 1 2; do
    "$BIN" tx sign "$WORK/a.json" --multisig "$admin" --from operator --sign-mode amino-json \
      --home "$H/node$k" "${KR[@]}" --output-document "$WORK/s$k.json"
  done
  "$BIN" tx multi-sign "$WORK/a.json" admin "$WORK/s0.json" "$WORK/s1.json" "$WORK/s2.json" \
    --home "$H/node0" "${KR[@]}" --output-document "$WORK/m.json"
  wait_tx "$("$BIN" tx broadcast "$WORK/m.json" --home "$H/node0" -o json)"
}

# Bitcoin (regtest): btc <i> <bitcoin-cli args>, mine <n>, faucet <address> <btc> → txid.
btc() { bash "$ROOT/localnet/btc.sh" "$@"; }
mine() { bash "$ROOT/localnet/mine.sh" "$1" >/dev/null; }
faucet() { btc 0 -rpcwallet=faucet sendtoaddress "$1" "$2"; }
btc_ready() { # wait until all 4 bitcoinds answer and agree on the tip
  local start=$SECONDS
  until [ "$(for i in 0 1 2 3; do btc "$i" getbestblockhash 2>/dev/null; done | sort -u | wc -l)" = "1" ] \
    && btc 3 getbestblockhash >/dev/null 2>&1; do
    [ $((SECONDS - start)) -lt 120 ] || fail "bitcoinds not in sync"
    sleep 2
  done
}

# block_event <node> <height> <type>: the first event of type at height, as a JSON
# object of its attributes (empty if none).
block_event() {
  curl -fsS "${RPCS[$1]}/block_results?height=$2" | jq -c --arg t "$3" '[.result.finalize_block_events[]? | select(.type==$t)
    | (.attributes | map({(.key): .value}) | add)] | .[0] // empty'
}

# find_event <node> <from> <to> <type> <jq-filter>: prints "height json" of the first
# matching event in [from, to], or nothing.
find_event() {
  local n=$1 from=$2 to=$3 typ=$4 filter=$5 h ev
  for ((h = from; h <= to; h++)); do
    ev=$(curl -fsS "${RPCS[$n]}/block_results?height=$h" | jq -c --arg t "$typ" "[.result.finalize_block_events[]? | select(.type==\$t)
      | (.attributes | map({(.key): .value}) | add) | select($filter)] | .[0] // empty")
    if [ -n "$ev" ]; then echo "$h $ev"; return 0; fi
  done
}

btc_sat() { awk -v a="$1" 'BEGIN { printf "%.0f", a * 100000000 }'; }

# fund_vault <vault-id> <btc>: send from the faucet to the vault's deposit address, mine
# 6 blocks and wait until the vault's collateral has grown by the amount.
fund_vault() {
  local id=$1 amt=$2 addr before want start
  addr=$(q custody deposit-address "$id" | jq -r .address)
  before=$(collateral "$id")
  want=$((before + $(btc_sat "$amt")))
  faucet "$addr" "$amt" >/dev/null
  mine 6
  start=$SECONDS
  until [ "$(collateral "$id")" = "$want" ]; do
    [ $((SECONDS - start)) -lt 120 ] || fail "vault $id never reached $want sat (at $(collateral "$id"))"
    sleep 2
  done
}
