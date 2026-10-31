#!/usr/bin/env bash
# Reproducible release build (spec M6-T2): keystoned for each target with the pinned Go
# toolchain, cgo through zig cc against the FROST library built by `make zf-lib-all`
# (crypto/frost/internal/zf/lib/<os>_<arch>), -trimpath, no build id or VCS stamp,
# version and commit set by -ldflags, and no build tags (byzantine, invariants and
# test-only code stay out). darwin is not built: cgo there needs Apple's SDK.
# Writes build/release/keystoned-<os>-<arch>[.exe] and build/release/SHA256SUMS.
# With --verify it builds linux/amd64 twice in separate directories and fails unless
# the two binaries are identical.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
GO=${GO:-go}
PINNED=$(cat "$ROOT/.go-version")
have=$("$GO" env GOVERSION)
[ "$have" = "$PINNED" ] || { echo "release needs $PINNED (.go-version), $GO is $have" >&2; exit 1; }
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
COMMIT=$(git rev-parse HEAD)
LDFLAGS="-s -w -buildid= \
 -X github.com/cosmos/cosmos-sdk/version.Name=keystone \
 -X github.com/cosmos/cosmos-sdk/version.AppName=keystoned \
 -X github.com/cosmos/cosmos-sdk/version.Version=$VERSION \
 -X github.com/cosmos/cosmos-sdk/version.Commit=$COMMIT"

ZIG=${ZIG:-zig}
zig_target() {
  case "$1/$2" in
    linux/amd64) echo x86_64-linux-gnu.2.36 ;;
    linux/arm64) echo aarch64-linux-gnu.2.36 ;;
    windows/amd64) echo x86_64-windows-gnu ;;
    *) echo "no FROST library target for $1/$2" >&2; return 1 ;;
  esac
}

build() { # build <os> <arch> <out>
  local zt
  zt=$(zig_target "$1" "$2")
  GOOS=$1 GOARCH=$2 CGO_ENABLED=1 CC="$ZIG cc -target $zt" GOFLAGS= \
    "$GO" build -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o "$3" ./cmd/keystoned
}

OUT=$ROOT/build/release
rm -rf "$OUT"; mkdir -p "$OUT"
for target in linux/amd64 linux/arm64 windows/amd64; do
  os=${target%/*} arch=${target#*/}
  ext=""; [ "$os" = windows ] && ext=.exe
  build "$os" "$arch" "$OUT/keystoned-$os-$arch$ext"
done
(cd "$OUT" && sha256sum keystoned-* > SHA256SUMS)
cat "$OUT/SHA256SUMS"

if [ "${1:-}" = "--verify" ]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  build linux amd64 "$tmp/keystoned"
  a=$(sha256sum < "$OUT/keystoned-linux-amd64" | cut -d' ' -f1)
  b=$(sha256sum < "$tmp/keystoned" | cut -d' ' -f1)
  [ "$a" = "$b" ] || { echo "not reproducible: $a vs $b" >&2; exit 1; }
  echo "reproducible: linux/amd64 rebuilt with the same sha256 $a"
fi
