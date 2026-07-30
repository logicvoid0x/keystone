#!/usr/bin/env bash
# mine.sh N: mines N regtest blocks on bitcoin0 to the "faucet" wallet (created on
# first use). Mine 101 once so the faucet has spendable coins.
# faucet.sh-style use: localnet/btc.sh 0 -rpcwallet=faucet sendtoaddress <addr> <btc>
set -euo pipefail
n=${1:-1}
BTC="$(dirname "$0")/btc.sh"
if ! "$BTC" 0 listwallets | grep -q '"faucet"'; then
  "$BTC" 0 loadwallet faucet >/dev/null 2>&1 || "$BTC" 0 createwallet faucet >/dev/null
fi
addr=$("$BTC" 0 -rpcwallet=faucet getnewaddress)
"$BTC" 0 generatetoaddress "$n" "$addr" >/dev/null
"$BTC" 0 getblockcount
