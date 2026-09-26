#!/usr/bin/env bash
# M5 demo (docs/specs/M5-robustness.md, acceptance check 2) on the docker localnet:
#   a. an attempt to remove 2 of 4 operators at once is rejected by the custody guard;
#   b. a signer is stopped mid-session (its node frozen); the session retries and signs;
#   c. a withdrawal stuck with a too-low feerate is replaced via RBF and confirms;
#   d. with funds in custody the admin removes node3; custody_membership_pending
#      appears; after the delay a new epoch forms among the other three, every UTXO is
#      swept to it and the old epoch reaches RETIRED.
# Needs: Docker, curl, jq. Leaves the localnet stopped (make localnet-clean to reset).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
WORK=$ROOT/.demo/M5
# shellcheck source=lib/localnet.sh
. "$ROOT/scripts/lib/localnet.sh"
COMPOSE=(docker compose -f "$ROOT/localnet/docker-compose.yml")

rm -rf "$WORK"; mkdir -p "$WORK"
make --no-print-directory localnet-clean >/dev/null 2>&1 || true
make --no-print-directory localnet-start >"$WORK/start.log" 2>&1 || { tail -20 "$WORK/start.log"; fail "make localnet-start"; }
make --no-print-directory localnet-monitoring >"$WORK/monitoring.log" 2>&1 || { tail -20 "$WORK/monitoring.log"; fail "make localnet-monitoring"; }
pass "localnet started (4 nodes, bitcoinds, signers, Prometheus + Grafana)"
wait_height 3 300
btc_ready
mine 101
btc_ready

# wait_event <node> <from> <blocks> <type> <jq filter>: "height json" of the first match.
wait_event() {
  local n=$1 from=$2 blocks=$3 typ=$4 filter=$5 h hit=""
  for ((h = from; h <= from + blocks; h++)); do
    wait_height "$h" 180
    hit=$(find_event "$n" "$h" "$h" "$typ" "$filter")
    if [ -n "$hit" ]; then echo "$hit"; return 0; fi
  done
}
# mine_until <seconds> <cmd...>: mine one block every 4 s until cmd succeeds.
mine_until() {
  local limit=$1 start=$SECONDS; shift
  until "$@"; do
    [ $((SECONDS - start)) -lt "$limit" ] || return 1
    mine 1
    sleep 4
  done
}
# atx_try <module> <cmd> [args...]: admin multisig tx; prints the broadcast/inclusion result.
atx_try() {
  local admin; admin=$("$BIN" keys show admin -a --home "$H/node0" --keyring-backend test)
  "$BIN" tx "$@" --from admin --generate-only --home "$H/node0" "${KR[@]}" --gas 2000000 > "$WORK/a.json"
  for k in 0 1 2; do
    "$BIN" tx sign "$WORK/a.json" --multisig "$admin" --from operator --sign-mode amino-json \
      --home "$H/node$k" "${KR[@]}" --output-document "$WORK/s$k.json"
  done
  "$BIN" tx multi-sign "$WORK/a.json" admin "$WORK/s0.json" "$WORK/s1.json" "$WORK/s2.json" \
    --home "$H/node0" "${KR[@]}" --output-document "$WORK/m.json"
  local out hash
  out=$("$BIN" tx broadcast "$WORK/m.json" --home "$H/node0" -o json)
  if [ "$(printf '%s' "$out" | jq -r .code)" != "0" ]; then
    printf '%s' "$out" # rejected by CheckTx
    return 0
  fi
  hash=$(printf '%s' "$out" | jq -r .txhash)
  "$BIN" q wait-tx "$hash" --timeout 60s --home "$H/node0" -o json
}
entry() { # entry <node> <power>
  local op pk
  op=$("$BIN" keys show operator -a --home "$H/node$1" --keyring-backend test)
  pk=$(q poa validators | jq -r --arg op "$op" '.validators[] | select(.operator_address == $op) | .consensus_pubkey')
  jq -nc --arg op "$op" --arg pk "$pk" --arg p "$2" --arg m "node$1" '{operator_address: $op, consensus_pubkey: $pk, moniker: $m, power: $p}'
}

# Funds in custody: 1 BTC in vault 1 and a 5,000 sat deposit (below MinDepositSat:
# it funds the fee reserve that pays sweep fees).
utx 0 test vault open-vault
fund_vault 1 1
ADDR=$(q custody deposit-address 1 | jq -r .address)
faucet "$ADDR" 0.00005 >/dev/null
mine 6
for _ in $(seq 1 60); do [ "$(q custody treasury | jq -r .fee_reserve_sat)" -ge 5000 ] && break; sleep 2; done
for _ in $(seq 1 30); do [ "$(price_field status)" = "PRICE_STATUS_ACTIVE" ] && break; sleep 2; done
pass "vault 1 holds 1 BTC; fee reserve $(q custody treasury | jq -r .fee_reserve_sat) sat"
btc 1 createwallet recv >/dev/null
RECV_SCRIPT=$(btc 1 -rpcwallet=recv getaddressinfo "$(btc 1 -rpcwallet=recv getnewaddress "" bech32)" | jq -r .scriptPubKey)

# a. Removing 2 of 4 at once leaves 2 < t = 3 of a funded epoch: rejected.
# Entries must be sorted by operator address bytes: q poa validators lists them so.
mapfile -t TWO < <(q poa validators | jq -c '.validators[-2:][] | {operator_address, consensus_pubkey, moniker, power: "0"}')
res=$(atx_try poa set-validators "${TWO[@]}")
[ "$(printf '%s' "$res" | jq -r .code)" != "0" ] || fail "removing 2 of 4 operators was accepted"
printf '%s' "$res" | jq -r .raw_log | grep -q 'removal guard' || fail "not rejected by the guard: $(printf '%s' "$res" | jq -r .raw_log)"
[ "$(q poa validators | jq '[.validators[] | select(.power == "1")] | length')" = "4" ] || fail "operator set changed"
pass "removing 2 of 4 operators at once rejected by the custody removal guard"

# b. Stop a signer mid-session: freeze (docker pause) a node that the next session will
# assign (signers are the first t operators by address with no misses), before the batch
# opens. Either its VE is missing when the batch opens, so the session assigns the
# others, or it was assigned, the session fails at its deadline and the retry signs.
OPS=$(q poa validators | jq -r '.validators[].operator_address' | tr -d '\r') # Windows jq ends lines with CRLF
OP0=$("$BIN" keys show operator -a --home "$H/node0" --keyring-backend test)
VICTIM=""
for op in $(printf '%s\n' "$OPS" | head -3); do
  [ "$op" = "$OP0" ] && continue
  for i in 1 2 3; do
    [ "$("$BIN" keys show operator -a --home "$H/node$i" --keyring-backend test)" = "$op" ] && VICTIM=node$i
  done
  [ -n "$VICTIM" ] && break
done
[ -n "$VICTIM" ] || fail "no assignable node to freeze"
H0=$(height)
utx 0 test vault withdraw-collateral 1 10000000 "$RECV_SCRIPT"
"${COMPOSE[@]}" pause "$VICTIM" >/dev/null 2>&1 # frozen (keeps its IP, so peers reconnect)
hit=$(wait_event 0 "$H0" 30 batch_opened '.batch == "1"')
[ -n "$hit" ] || fail "batch 1 did not open"
HO=${hit%% *}
hit=$(wait_event 0 "$HO" 40 withdrawal_signed '.batch == "1"')
[ -n "$hit" ] || fail "batch 1 not signed with $VICTIM frozen"
failed=$(find_event 0 "$HO" "${hit%% *}" session_failed '.batch == "1"' || true)
note="$VICTIM was left out of the session (no VE at the open)"
[ -n "$failed" ] && note="its session failed at ${failed%% *} ($(printf '%s' "${failed#* }" | jq -r .reason)), the retry signed"
"${COMPOSE[@]}" unpause "$VICTIM" >/dev/null 2>&1
confirmed1() { [ -n "$(find_event 0 "$HO" "$(height)" tx_confirmed '.batch == "1"')" ]; }
mine_until 120 confirmed1 || fail "batch 1 not confirmed"
pass "$VICTIM frozen mid-session (docker pause): batch 1 signed at ${hit%% *}; $note; confirmed"

# c. RBF: bitcoin0 (the only miner) deprioritises the first version so it is not mined.
H1=$(height)
utx 0 test vault withdraw-collateral 1 10000000 "$RECV_SCRIPT"
hit=$(wait_event 0 "$H1" 40 withdrawal_signed '.batch == "2"')
[ -n "$hit" ] || fail "batch 2 not signed"
TXA=$(printf '%s' "${hit#* }" | jq -r .txid)
start=$SECONDS
until btc 0 getrawmempool | jq -e --arg t "$TXA" 'index($t) != null' >/dev/null; do
  [ $((SECONDS - start)) -lt 90 ] || fail "$TXA never reached the mempool"; sleep 2
done
btc 0 prioritisetransaction "$TXA" 0.0 -100000000 >/dev/null
H2=$(height)
mine 7
btc_ready
hit=$(wait_event 0 "$H2" 40 rbf_started '.batch == "2"')
[ -n "$hit" ] || fail "no RBF for batch 2"
TXB=$(printf '%s' "${hit#* }" | jq -r .txid)
RATE=$(printf '%s' "${hit#* }" | jq -r .feerate)
hit=$(wait_event 0 "${hit%% *}" 40 withdrawal_signed ".batch == \"2\" and .txid == \"$TXB\"")
[ -n "$hit" ] || fail "RBF version of batch 2 not signed"
confirmed2() { [ -n "$(find_event 0 "$H2" "$(height)" tx_confirmed ".batch == \"2\" and .txid == \"$TXB\"")" ]; }
mine_until 120 confirmed2 || fail "RBF version not confirmed"
pass "batch 2 stuck at 2 sat/vB ($TXA) replaced by $TXB at $RATE sat/vB, which confirmed"

# d. Membership change: remove node3; the new epoch takes custody.
H3=$(height)
res=$(atx_try poa set-validators "$(entry 3 0)")
[ "$(printf '%s' "$res" | jq -r .code)" = "0" ] || fail "removing node3: $(printf '%s' "$res" | jq -r .raw_log)"
hit=$(wait_event 0 "$H3" 10 custody_membership_pending '.removed != ""')
[ -n "$hit" ] || fail "no custody_membership_pending"
EFF=$(printf '%s' "${hit#* }" | jq -r .effective_height)
pass "node3 removed (power 0); custody_membership_pending at ${hit%% *}, effective at $EFF"
wait_height "$EFF" 900
hit=$(wait_event 0 "$EFF" 90 epoch_activated '.epoch == "2"')
[ -n "$hit" ] || fail "epoch 2 not activated"
[ "$(q custody active-epoch | jq -r .epoch.n)" = "3" ] || fail "epoch 2 is not among 3 operators"
pass "epoch 2 ACTIVE at ${hit%% *} among 3 operators; epoch 1 RETIRING"
retired() { [ -n "$(find_event 0 "$EFF" "$(height)" epoch_retired '.epoch == "1"')" ]; }
mine_until 600 retired || fail "epoch 1 did not retire"
sweeps=0
for ((h = EFF; h <= $(height); h++)); do
  [ -n "$(find_event 0 "$h" "$h" batch_opened '.kind == "BATCH_KIND_SWEEP"')" ] && sweeps=$((sweeps + 1))
done
TREAS=$(q custody treasury)
pass "epoch 1 RETIRED after $sweeps sweep batch(es); custodied $(printf '%s' "$TREAS" | jq -r .custodied_sat) sat, deficit $(printf '%s' "$TREAS" | jq -r .deficit_sat)"

# Monitoring sees the nodes and loads the alert rules.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=keystone_custodied_sat' | jq -e '.data.result | length >= 3' >/dev/null || fail "Prometheus has no keystone_custodied_sat"
curl -fsS http://127.0.0.1:9095/api/v1/rules | jq -e '[.data.groups[].rules[]] | length == 8' >/dev/null || fail "alert rules not loaded"
curl -fsS 'http://127.0.0.1:3000/api/dashboards/uid/keystone' | jq -e '.dashboard.panels | length == 18' >/dev/null || fail "Grafana dashboard not provisioned"
pass "Prometheus scrapes the nodes with the 8 §16 alerts; Grafana serves the Keystone dashboard"

last=$(( $(height) - 1 ))
for i in 1 2; do
  [ "$(curl -fsS "${RPCS[$i]}/block?height=$last" | jq -r .result.block.header.app_hash)" = \
    "$(curl -fsS "${RPCS[0]}/block?height=$last" | jq -r .result.block.header.app_hash)" ] || fail "node$i app hash differs at $last"
done
pass "identical app hashes at height $last"
make --no-print-directory localnet-stop >/dev/null 2>&1 || true
echo "M5 demo passed"
