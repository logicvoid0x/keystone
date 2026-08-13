#!/usr/bin/env bash
# Runs `go test` and passes its exit code through, with two Windows exceptions:
#  - antivirus can hold a finished test binary open so that `go` fails to delete it
#    ("go: unlinkat ...: being used by another process") and exits 1 after every test
#    passed. When that is the only error and nothing FAILed, exit 0 with a warning.
#  - Smart App Control can refuse to start a particular test binary ("An Application
#    Control policy has blocked this file"), keyed on the file's hash. Each package
#    that failed only that way is run once more, built with an extra -X flag so the
#    binary differs; its result counts.
set -uo pipefail

GO=${GO:-go}
log=$(mktemp)
trap 'rm -f "$log"' EXIT

"$GO" test "$@" 2>&1 | tee "$log"
rc=${PIPESTATUS[0]}
[ "$rc" -eq 0 ] && exit 0

# Packages whose test binary was blocked: the FAIL line right after the fork/exec error.
blocked=$(awk '/Application Control policy has blocked this file/ { b = 1; next }
  b && /^FAIL\t/ { print $2; b = 0 }' "$log")
if [ -n "$blocked" ]; then
  # Every other FAIL must be one of the blocked packages.
  others=$(grep -E '^(--- FAIL|panic:)' "$log"; grep -E '^FAIL\t' "$log" | awk '{print $2}' | grep -vxF "$blocked")
  if [ -z "$others" ]; then
    args=()
    for a in "$@"; do case "$a" in ./...) ;; *) args+=("$a") ;; esac; done
    echo "gotest.sh: retrying packages blocked by Application Control: $blocked" >&2
    # shellcheck disable=SC2086
    "$GO" test "${args[@]}" -ldflags=-X=main.appControlRetry=1 $blocked
    exit $?
  fi
fi

if grep -Eq '^(FAIL|--- FAIL|panic:)' "$log"; then
  exit "$rc"
fi
# Any line that is not a package result or the known cleanup error means a real failure.
if grep -Ev '^(ok |\?   |go: unlinkat .*being used by another process\.)' "$log" | grep -q .; then
  exit "$rc"
fi
if grep -q '^go: unlinkat' "$log"; then
  echo "gotest.sh: all packages passed; ignoring Windows test-binary cleanup error (unlinkat)" >&2
  exit 0
fi
exit "$rc"
