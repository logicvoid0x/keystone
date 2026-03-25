#!/usr/bin/env bash
# Regenerates gogoproto Go code for proto/keystone/**. Needs buf, protoc-gen-gocosmos
# and protoc-gen-grpc-gateway on PATH (`make proto-tools`).
set -euo pipefail

cd "$(dirname "$0")/../proto"
if ! find ./keystone -name '*.proto' 2>/dev/null | grep -q .; then
  echo "no keystone protos yet"
  exit 0
fi

buf generate --template buf.gen.gogo.yaml --path keystone

cd ..
cp -r github.com/OWNER/keystone/* ./
rm -rf github.com
