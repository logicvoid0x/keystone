#!/usr/bin/env bash
# M4a demo (docs/specs/M4a-frost-signing.md, acceptance check 2) on the docker localnet
# (4 operators, a regtest bitcoind and a FROST signer each, a dealer key in genesis):
#   1. deposit 1 BTC to vault 1, mint, then MsgWithdrawCollateral 0.2 BTC to a regtest
#      address;
#   2. within about 25 Keystone blocks the batch opens and withdrawal_signed appears;
#   3. the tx is in the regtest mempool; mine 1 block; tx_confirmed follows; the
#      recipient wallet shows 0.2 BTC minus its fee share;
#   4. with node3's signer stopped, another withdrawal still signs (3 of 4 = t).
# Needs: Docker, curl, jq. Leaves the localnet stopped (make localnet-clean to reset).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
WORK=$ROOT/.demo/M4a
# shellcheck source=lib/localnet.sh
. "$ROOT/scripts/lib/localnet.sh"

rm -rf "$WORK"; mkdir -p "$WORK"
# KEYSTONE_DEMO_REUSE=1 runs on an already started localnet (demo-M4b, after the DKG).
if [ -z "${KEYSTONE_DEMO_REUSE:-}" ]; then
  make --no-print-directory localnet-clean >/dev/null 2>&1 || true
  make --no-print-directory localnet-start >"$WORK/start.log" 2>&1 || { tail -20 "$WORK/start.log"; fail "make localnet-start"; }
  pass "localnet started (4 nodes, 4 regtest bitcoinds, 4 FROST signers, mockfeed)"
fi
wait_height 3 300
btc_ready
mine 101 # faucet coins mature
btc_ready

# Recipient wallet on bitcoin1 (not the faucet's node).
btc 1 createwallet recv >/dev/null
RECV=$(btc 1 -rpcwallet=recv getnewaddress "" bech32)
RECV_SCRIPT=$(btc 1 -rpcwallet=recv getaddressinfo "$RECV" | jq -r .scriptPubKey)
RECV2=$(btc 1 -rpcwallet=recv getnewaddress "" bech32m)
RECV2_SCRIPT=$(btc 1 -rpcwallet=recv getaddressinfo "$RECV2" | jq -r .scriptPubKey)

# withdrawal_signed{batch} at a height in [from, to] on node n, or nothing.
wait_event() { # $1 node, $2 from, $3 blocks, $4 type, $5 jq filter → "height json"
  local n=$1 from=$2 blocks=$3 typ=$4 filter=$5 h hit=""
  wait_height $((from + 1)) 120
  for ((h = from; h <= from + blocks; h++)); do
    wait_height "$h" 120
    hit=$(find_event "$n" "$h" "$h" "$typ" "$filter")
    if [ -n "$hit" ]; then echo "$hit"; return 0; fi
  done
}

# 1. Vault 1 with 1 BTC, 30,000 KUSD minted, then a 0.2 BTC withdrawal.
utx 0 test vault open-vault
fund_vault 1 1
for _ in $(seq 1 30); do [ "$(price_field status)" = "PRICE_STATUS_ACTIVE" ] && break; sleep 2; done
utx 0 test vault mint 1 30000000000
pass "vault 1: 1 BTC deposited (regtest), 30,000 KUSD minted"
H0=$(height)
utx 0 test vault withdraw-collateral 1 20000000 "$RECV_SCRIPT"
REQ=$(q custody withdrawal 1)
[ "$(printf '%s' "$REQ" | jq -r .request.status)" = "REQUEST_STATUS_QUEUED" ] || fail "request 1: $REQ"
[ "$(collateral 1)" = "80000000" ] || fail "vault 1 collateral $(collateral 1), want 80000000"
pass "withdraw-collateral 0.2 BTC to $RECV (request 1 queued at height $H0; collateral 0.8 BTC)"

# 2. The batch opens after BatchMaxWait (20) blocks and signs within a few more.
hit=$(wait_event 0 "$H0" 30 withdrawal_signed '.batch == "1"')
[ -n "$hit" ] || fail "no withdrawal_signed for batch 1 within 30 blocks of $H0"
HS=${hit%% *}
TXID=$(printf '%s' "${hit#* }" | jq -r .txid)
opened=$(find_event 0 "$H0" "$HS" batch_opened '.batch == "1"')
for i in 1 2 3; do
  [ -n "$(find_event "$i" "$HS" "$HS" withdrawal_signed '.batch == "1"')" ] || fail "node$i: no withdrawal_signed at $HS"
done
pass "batch 1 opened at height ${opened%% *}, withdrawal_signed at $HS (+$((HS - H0)) blocks) on all 4 nodes; txid $TXID"

# 3. The watchers broadcast it; it is in the mempool; one block confirms it.
start=$SECONDS
until btc 0 getrawmempool | jq -e --arg t "$TXID" 'index($t) != null' >/dev/null; do
  [ $((SECONDS - start)) -lt 90 ] || fail "$TXID never reached bitcoin0's mempool"
  sleep 2
done
pass "$TXID is in the regtest mempool"
H1=$(height)
mine 1
btc_ready
hit=$(wait_event 0 "$H1" 15 tx_confirmed '.batch == "1"')
[ -n "$hit" ] || fail "no tx_confirmed for batch 1"
FEE=$(q custody withdrawal 1 | jq -r .request.fee_sat)
[ "$(q custody withdrawal 1 | jq -r .request.status)" = "REQUEST_STATUS_COMPLETED" ] || fail "request 1 not COMPLETED"
GOT=$(btc 1 -rpcwallet=recv getreceivedbyaddress "$RECV" 1)
WANT=$(awk -v f="$FEE" 'BEGIN { printf "%.8f", (20000000 - f) / 100000000 }')
[ "$GOT" = "$WANT" ] || fail "recipient received $GOT BTC, want $WANT"
pass "tx_confirmed at height ${hit%% *}; request 1 COMPLETED; recipient received $GOT BTC (0.2 BTC − $FEE sat fee share)"

# 4. node3's signer stopped: 3 of 4 signers = t still sign.
make --no-print-directory localnet-signer3 SIGNER=false >/dev/null 2>&1
start=$SECONDS
until h3=$(height "${RPCS[3]}") && [ "$h3" -gt 0 ] && [ "$h3" -ge $(($(height) - 1)) ]; do
  [ $((SECONDS - start)) -lt 120 ] || fail "node3 did not come back"
  sleep 2
done
H2=$(height)
utx 0 test vault withdraw-collateral 1 10000000 "$RECV2_SCRIPT"
hit=$(wait_event 0 "$H2" 40 withdrawal_signed '.batch == "2"')
[ -n "$hit" ] || fail "no withdrawal_signed for batch 2 with node3's signer stopped"
HS2=${hit%% *}
failed=$(find_event 0 "$H2" "$HS2" session_failed '.batch == "2"' || true)
note="all assigned signers answered"
[ -n "$failed" ] && note="a session missing node3 failed at ${failed%% *} and its retry signed"
pass "node3 signer stopped: batch 2 (0.1 BTC to $RECV2) withdrawal_signed at $HS2; $note"
mine 1
btc_ready
hit=$(wait_event 0 "$(height)" 15 tx_confirmed '.batch == "2"')
[ -n "$hit" ] || fail "no tx_confirmed for batch 2"
make --no-print-directory localnet-signer3 SIGNER=true >/dev/null 2>&1
pass "batch 2 confirmed at height ${hit%% *}; node3 signer restarted"

# Identical app hashes across the run.
wait_height $(($(height) + 2)) 60
last=$(( $(height) - 1 ))
for i in 1 2 3; do
  wait_height "$last" 60
  [ "$(curl -fsS "${RPCS[$i]}/block?height=$last" | jq -r .result.block.header.app_hash)" = \
    "$(curl -fsS "${RPCS[0]}/block?height=$last" | jq -r .result.block.header.app_hash)" ] || fail "node$i app hash differs at $last"
done
pass "identical app hashes on all 4 nodes at height $last"

make --no-print-directory localnet-stop >/dev/null 2>&1 || true
pass "localnet stopped (homes kept in localnet/homes)"
echo "M4a demo passed"
