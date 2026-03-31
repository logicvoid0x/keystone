#!/usr/bin/env bash
# Runs `go test` and passes its exit code through, with one exception: on Windows,
# antivirus can hold a finished test binary open so that `go` fails to delete it
# ("go: unlinkat ...: being used by another process") and exits 1 after every test
# passed. When that is the only error and nothing FAILed, exit 0 with a warning.
set -uo pipefail

GO=${GO:-go}
log=$(mktemp)
trap 'rm -f "$log"' EXIT

"$GO" test "$@" 2>&1 | tee "$log"
rc=${PIPESTATUS[0]}
[ "$rc" -eq 0 ] && exit 0

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
