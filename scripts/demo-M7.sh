#!/usr/bin/env bash
# M7 demo (docs/specs/M7-pos.md, acceptance check 6) on the docker localnet:
#   1. blocks carry tx0 and VEs verify with x/staking as the validator store;
#   2. a delegation changes node0's voting power (x/staking -> CometBFT);
#   3. funds in custody (1 BTC in vault 1);
#   4. scripts/add-validator.sh: node4 is refused until allowlisted, then bonds 1.5x the
#      others' stake and signs blocks; it registers custody keys, enters the top N
#      past the entry margin (custody_membership_pending), and after the delay a new
#      epoch forms by DKG, the old epoch's UTXOs are swept to it and it retires;
#   5. stakers earn ukey inflation (x/mint -> x/distribution) and gas stays zero.
# Theft slashing (§9.12) is covered by app and custody tests rather than by this demo.
# Needs: Docker, curl, jq. Leaves the localnet stopped (make localnet-clean to reset).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
WORK=$ROOT/.demo/M7
# shellcheck source=lib/localnet.sh
. "$ROOT/scripts/lib/localnet.sh"
COMPOSE=(docker compose -f "$ROOT/localnet/docker-compose.yml")

rm -rf "$WORK"; mkdir -p "$WORK"
make --no-print-directory localnet-clean >/dev/null 2>&1 || true
make --no-print-directory localnet-start >"$WORK/start.log" 2>&1 || { tail -20 "$WORK/start.log"; fail "make localnet-start"; }
pass "localnet started (4 validators, bitcoinds, signers)"
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
mine_until() {
  local limit=$1 start=$SECONDS; shift
  until "$@"; do
    [ $((SECONDS - start)) -lt "$limit" ] || return 1
    mine 1
    sleep 4
  done
}
op_of() { "$BIN" keys show operator -a --home "$H/node$1" --keyring-backend test | tr -d '\r'; }
val_of() { "$BIN" keys show operator -a --bech val --home "$H/node$1" --keyring-backend test | tr -d '\r'; }
comet_power() { # comet_power <height> <cons address hex>
  curl -fsS "${RPCS[0]}/validators?height=$1&per_page=100" | jq -r --arg a "$2" '.result.validators[] | select(.address == $a) | .voting_power' | tr -d '\r'
}

# 1. tx0 and VEs.
h=$(height)
ev=$(block_event 0 "$h" protocol_votes)
[ -n "$ev" ] || fail "no protocol_votes event at $h"
vp=$(printf '%s' "$ev" | jq -r .verified_power | tr -d '\r'); tp=$(printf '%s' "$ev" | jq -r .total_power | tr -d '\r')
[ $((vp * 3)) -gt $((tp * 2)) ] || fail "verified VE power $vp of $tp"
pass "height $h: tx0 with verified VE power $vp of $tp (x/staking validator store)"

# 2. Delegation moves power.
VAL0=$(val_of 0)
CONS0=$(curl -fsS "${RPCS[0]}/status" | jq -r .result.validator_info.address | tr -d '\r')
before=$(comet_power "$(height)" "$CONS0")
utx 1 test staking delegate "$VAL0" 200000000000ukey
H1=$(height)
wait_height $((H1 + 3)) 120
after=$(comet_power "$((H1 + 3))" "$CONS0")
[ "$after" -gt "$before" ] || fail "node0 power $before -> $after"
pass "delegation of 200,000,000,000 ukey: node0 voting power $before -> $after"

# 3. Funds in custody.
utx 0 test vault open-vault
fund_vault 1 1
# A 5,000 sat deposit (below MinDepositSat) funds the fee reserve that pays sweep fees.
faucet "$(q custody deposit-address 1 | jq -r .address | tr -d '\r')" 0.00005 >/dev/null
mine 6
for _ in $(seq 1 60); do [ "$(q custody treasury | jq -r .fee_reserve_sat | tr -d '\r')" -ge 5000 ] && break; sleep 2; done
pass "vault 1 holds 1 BTC (epoch 1); fee reserve $(q custody treasury | jq -r .fee_reserve_sat | tr -d '\r') sat"

# 4. node4: allowlist, bond, custody keys, top-N entry, new epoch.
BOND=1500000000000 bash "$ROOT/scripts/add-validator.sh" | sed 's/^/      /' || fail "add-validator.sh"
N4="$ROOT/.demo/add-validator/node4.json"
ENC=$(jq -r .enc_pubkey_hex "$N4" | tr -d '\r'); HOT=$(jq -r .hot_key "$N4" | tr -d '\r'); OP4=$(jq -r .operator_address "$N4" | tr -d '\r')
H3=$(height)
wait_tx "$("$BIN" tx custody register-custody-key "$ENC" "$HOT" --from operator --home "$H/node4" "${KR[@]}" --gas 400000 --yes -o json --node "${RPCS[0]}")"
pass "node4 registered its custody keys"
hit=$(wait_event 0 "$H3" 30 custody_membership_pending '.added != ""')
[ -n "$hit" ] || fail "no custody_membership_pending after node4 entered the top N"
added=$(printf '%s' "${hit#* }" | jq -r .added | tr -d '\r'); removed=$(printf '%s' "${hit#* }" | jq -r .removed | tr -d '\r')
EFF=$(printf '%s' "${hit#* }" | jq -r .effective_height | tr -d '\r')
[ "$added" = "$OP4" ] || fail "added $added, want node4 $OP4"
[ -n "$removed" ] || fail "nobody leaves the top N=4"
pass "node4 (1.5x stake) enters the custody set past the margin, displacing $removed; effective at $EFF"
wait_height "$EFF" 900
hit=$(wait_event 0 "$EFF" 120 epoch_activated '.epoch == "2"')
[ -n "$hit" ] || fail "epoch 2 not activated"
pass "epoch 2 ACTIVE at ${hit%% *} by DKG among the new custody set"
retired() { [ -n "$(find_event 0 "$EFF" "$(height)" epoch_retired '.epoch == "1"')" ]; }
mine_until 600 retired || fail "epoch 1 did not retire"
TREAS=$(q custody treasury)
[ "$(printf '%s' "$TREAS" | jq -r .deficit_sat | tr -d '\r')" = 0 ] || fail "deficit after the sweep: $TREAS"
pass "epoch 1 swept and RETIRED; custodied $(printf '%s' "$TREAS" | jq -r .custodied_sat) sat, deficit $(printf '%s' "$TREAS" | jq -r .deficit_sat)"

# 5. Rewards; zero gas.
# DecCoins print as strings ("<amount>ukey").
rew=$(q distribution validator-outstanding-rewards "$VAL0" | jq -r '[.rewards.rewards[]? | strings | select(endswith("ukey")) | rtrimstr("ukey")][0] // "0"' | tr -d '\r')
awk -v r="$rew" 'BEGIN { exit !(r + 0 > 0) }' || fail "node0 has no ukey rewards"
pass "node0 outstanding rewards: $rew ukey (x/mint inflation via x/distribution)"
out=$("$BIN" tx bank send test "$(op_of 2)" 1utest --from test --home "$H/node1" "${KR[@]}" --yes -o json)
wait_tx "$out"
pass "a user tx with no fee is accepted (gas price zero)"

last=$(( $(height) - 1 ))
for i in 1 2; do
  [ "$(curl -fsS "${RPCS[$i]}/block?height=$last" | jq -r .result.block.header.app_hash)" = \
    "$(curl -fsS "${RPCS[0]}/block?height=$last" | jq -r .result.block.header.app_hash)" ] || fail "node$i app hash differs at $last"
done
pass "identical app hashes at height $last"
make --no-print-directory localnet-stop >/dev/null 2>&1 || true
echo "M7 demo passed"
