#!/usr/bin/env bash
# btc.sh <i> <bitcoin-cli args...>: runs bitcoin-cli against the localnet's bitcoin<i>.
set -euo pipefail
i=$1; shift
cd "$(dirname "$0")"
MSYS_NO_PATHCONV=1 docker compose -f docker-compose.yml exec -T "bitcoin$i" \
  bitcoin-cli -regtest -rpcuser=keystone -rpcpassword=keystone "$@"
