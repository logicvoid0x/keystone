#!/usr/bin/env bash
# M3 demo (docs/specs/M3-regtest-deposits.md, acceptance check 2) on the docker
# localnet (4 operators, a regtest bitcoind each, a dealer key in genesis):
#   1. open a vault, get its address, send 0.5 BTC from the faucet, mine 6 blocks;
#   2. within 5 Keystone blocks, collateral_credited{vault, 50000000} on all nodes;
#   3. mint 30,000 KUSD against it;
#   4. a deposit that would exceed the 10 BTC cap lands in claimable, not collateral;
#   5. reorg: deposit, mine 6, wait for credit, invalidateblock on every bitcoind back
#      past it, mine a longer chain without it; the deposit is reverted.
# Needs: Docker, curl, jq. Leaves the localnet stopped (make localnet-clean to reset).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
WORK=$ROOT/.demo/M3
# shellcheck source=lib/localnet.sh
. "$ROOT/scripts/lib/localnet.sh"

rm -rf "$WORK"; mkdir -p "$WORK"
make --no-print-directory localnet-clean >/dev/null 2>&1 || true
make --no-print-directory localnet-start >"$WORK/start.log" 2>&1 || { tail -20 "$WORK/start.log"; fail "make localnet-start"; }
pass "localnet started (4 nodes, 4 regtest bitcoinds, mockfeed)"
wait_height 3 300
btc_ready
mine 101 # faucet coins mature
btc_ready
epoch=$(q custody active-epoch)
pass "custody epoch $(printf '%s' "$epoch" | jq -r .epoch.number) ACTIVE (t=$(printf '%s' "$epoch" | jq -r .epoch.t) of n=$(printf '%s' "$epoch" | jq -r .epoch.n)), treasury $(printf '%s' "$epoch" | jq -r .treasury_address)"

# 1. Open vault 1 (A = node0 "test") and send it 0.5 BTC.
utx 0 test vault open-vault
ADDR=$(q custody deposit-address 1 | jq -r .address)
case "$ADDR" in bcrt1p*) ;; *) fail "deposit address $ADDR is not a regtest P2TR address" ;; esac
TXID=$(faucet "$ADDR" 0.5)
mine 6
btc_ready
H0=$(height)
pass "vault 1 opened; sent 0.5 BTC to $ADDR (txid ${TXID:0:16}…), mined 6 blocks at Keystone height $H0"

# 2. collateral_credited{1, 50000000} within 5 Keystone blocks, on all 4 nodes.
wait_height $((H0 + 5)) 120
cred_h=()
for i in 0 1 2 3; do
  hit=$(find_event "$i" "$H0" $((H0 + 5)) collateral_credited '.id == "1" and .sat == "50000000"')
  [ -n "$hit" ] || fail "node$i: no collateral_credited{1, 50000000} in heights $H0..$((H0 + 5))"
  cred_h+=("${hit%% *}")
done
[ "${cred_h[0]}" = "${cred_h[1]}" ] && [ "${cred_h[0]}" = "${cred_h[2]}" ] && [ "${cred_h[0]}" = "${cred_h[3]}" ] \
  || fail "credited at different heights: ${cred_h[*]}"
acc=$(block_event 0 "${cred_h[0]}" deposit_accepted)
[ "$(printf '%s' "$acc" | jq -r .outpoint | cut -d: -f1)" = "$TXID" ] || fail "deposit_accepted outpoint $(printf '%s' "$acc" | jq -r .outpoint) != $TXID"
pass "collateral_credited{1, 50000000} at height ${cred_h[0]} (+$((cred_h[0] - H0))) on all 4 nodes; deposit_accepted outpoint matches bitcoind"

# 3. Mint 30,000 KUSD.
for _ in $(seq 1 30); do [ "$(price_field status)" = "PRICE_STATUS_ACTIVE" ] && break; sleep 2; done
utx 0 test vault mint 1 30000000000
debt=$(q vault vault 1 | jq -r .vault.debt)
[ "$debt" -ge 30000000000 ] || fail "vault 1 debt $debt"
pass "minted 30,000 KUSD against vault 1 (debt $debt ukusd)"

# 4. 10 BTC more would take credited to 10.5 BTC, over the 10 BTC cap: all claimable.
A=$("$BIN" keys show test -a --home "$H/node0" --keyring-backend test)
claim0=$(q custody claimable "$A" | jq -r .claimable_sat)
H1=$(height)
faucet "$ADDR" 10 >/dev/null
mine 6
btc_ready
start=$SECONDS
until [ "$(q custody claimable "$A" | jq -r .claimable_sat)" = "$((claim0 + 1000000000))" ]; do
  [ $((SECONDS - start)) -lt 120 ] || fail "over-cap deposit never became claimable"
  sleep 2
done
[ "$(collateral 1)" = "50000000" ] || fail "vault 1 collateral changed to $(collateral 1)"
hit=$(find_event 0 "$H1" "$(height)" deposit_over_cap '.vault == "1" and .sat == "1000000000"')
[ -n "$hit" ] || fail "no deposit_over_cap event"
tr=$(q custody treasury)
pass "10 BTC over-cap deposit → claimable 10 BTC, collateral still 0.5 BTC (deposit_over_cap at ${hit%% *}; custodied $(printf '%s' "$tr" | jq -r .custodied_sat), credited $(printf '%s' "$tr" | jq -r .credited_sat), cap $(printf '%s' "$tr" | jq -r .cap_sat))"

# 5. Reorg: 0.2 BTC deposit, credited, then the block that holds it is invalidated on
# every bitcoind and a longer chain without it is mined.
TX2=$(faucet "$ADDR" 0.2)
mine 6
btc_ready
start=$SECONDS
until [ "$(collateral 1)" = "70000000" ]; do
  [ $((SECONDS - start)) -lt 120 ] || fail "0.2 BTC deposit never credited (collateral $(collateral 1))"
  sleep 2
done
BLK=$(btc 0 -rpcwallet=faucet gettransaction "$TX2" | jq -r .blockhash)
BLKH=$(btc 0 getblock "$BLK" | jq -r .height)
TIP=$(btc 0 getblockcount)
pass "0.2 BTC deposit credited (vault 1 collateral 0.7 BTC); it is in block $BLKH (tip $TIP)"
H2=$(height)
for i in 0 1 2 3; do btc "$i" invalidateblock "$BLK"; done
GEN=$(btc 0 -rpcwallet=faucet getnewaddress)
for ((n = 0; n <= TIP - BLKH + 1; n++)); do btc 0 generateblock "$GEN" '[]' >/dev/null; done
btc_ready
NEWTIP=$(btc 0 getblockcount)
[ "$NEWTIP" -gt "$TIP" ] || fail "new chain ($NEWTIP) is not longer than the old ($TIP)"
start=$SECONDS
until [ "$(collateral 1)" = "50000000" ]; do
  [ $((SECONDS - start)) -lt 120 ] || fail "deposit never reverted (collateral $(collateral 1))"
  sleep 2
done
rev_h=()
for i in 0 1 2 3; do
  hit=$(find_event "$i" "$H2" "$(height "${RPCS[$i]}")" deposit_reverted ".outpoint | startswith(\"$TX2\")")
  [ -n "$hit" ] || fail "node$i: no deposit_reverted for $TX2"
  rev_h+=("${hit%% *}")
done
[ "${rev_h[0]}" = "${rev_h[1]}" ] && [ "${rev_h[0]}" = "${rev_h[2]}" ] && [ "${rev_h[0]}" = "${rev_h[3]}" ] \
  || fail "reverted at different heights: ${rev_h[*]}"
def=$(q custody treasury | jq -r .deficit_sat)
pass "invalidated block $BLKH on all 4 bitcoinds, mined to $NEWTIP without the tx: deposit_reverted at height ${rev_h[0]} on all 4 nodes; collateral back to 0.5 BTC, deficit $def"

# Identical app hashes across the run.
last=$(( $(height) - 1 ))
for i in 1 2 3; do
  [ "$(curl -fsS "${RPCS[$i]}/block?height=$last" | jq -r .result.block.header.app_hash)" = \
    "$(curl -fsS "${RPCS[0]}/block?height=$last" | jq -r .result.block.header.app_hash)" ] || fail "node$i app hash differs at $last"
done
pass "identical app hashes on all 4 nodes at height $last"

make --no-print-directory localnet-stop >/dev/null 2>&1 || true
pass "localnet stopped (homes kept in localnet/homes)"
echo "M3 demo passed"
