#!/usr/bin/env bash
# Genesis ceremony (spec M6-T4). Each operator runs
#   keystoned genesis operator-info <moniker> > <moniker>.json
# and sends the JSON to the coordinator, who runs
#   scripts/genesis-ceremony.sh <chain-id> <genesis-time RFC3339> <dir with *.json>
# The script builds genesis.json (5-of-7 admin multisig for 7 operators, custody keys, no
# epoch: the DKG runs on-chain from height 2), validates it and prints its SHA-256 for
# every operator to compare before starting.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=${BIN:-$ROOT/build/keystoned}
[ -x "$BIN" ] || BIN="$BIN.exe"
[ $# -eq 3 ] || { echo "usage: $0 <chain-id> <genesis-time> <operator-info dir>" >&2; exit 2; }
CHAIN=$1 TIME=$2 DIR=$3
shopt -s nullglob
infos=("$DIR"/*.json)
[ ${#infos[@]} -ge 1 ] || { echo "no operator-info files in $DIR" >&2; exit 1; }
"$BIN" genesis ceremony "${infos[@]}" --chain-id "$CHAIN" --genesis-time "$TIME" --output "$DIR/genesis.json"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/config"; cp "$DIR/genesis.json" "$tmp/config/genesis.json"
"$BIN" genesis validate "$tmp/config/genesis.json" --home "$tmp"
echo "Publish genesis.json and its sha256; every operator checks: sha256sum genesis.json"
