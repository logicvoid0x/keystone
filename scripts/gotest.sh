#!/usr/bin/env bash
# Runs `go test` and passes its exit code through, with two Windows exceptions:
#  - antivirus can hold a finished test binary open so that `go` fails to delete it
#    ("go: unlinkat ...: being used by another process") and exits 1 after every test
#    passed. When that is the only error and nothing FAILed, exit 0 with a warning.
#  - Smart App Control can refuse to start a particular test binary ("An Application
#    Control policy has blocked this file"). Packages that failed only that way are run
#    again, up to 5 times, each time built with a different -X flag so the binary (and
#    its hash) differs; the result of the last run counts.
set -uo pipefail

GO=${GO:-go}
log=$(mktemp)
trap 'rm -f "$log"' EXIT

"$GO" test "$@" 2>&1 | tee "$log"
rc=${PIPESTATUS[0]}
[ "$rc" -eq 0 ] && exit 0

# Packages whose test binary was blocked: the FAIL line right after the fork/exec error.
blocked_in() {
  awk '/Application Control policy has blocked this file/ { b = 1; next }
    b && /^FAIL\t/ { print $2; b = 0 }' "$1"
}
blocked=$(blocked_in "$log")
if [ -n "$blocked" ]; then
  # Every other FAIL must be one of the blocked packages.
  others=$(grep -E '^(--- FAIL|panic:)' "$log"; grep -E '^FAIL\t' "$log" | awk '{print $2}' | grep -vxF "$blocked")
  if [ -z "$others" ]; then
    args=()
    for a in "$@"; do case "$a" in ./...) ;; *) args+=("$a") ;; esac; done
    for try in 1 2 3 4 5; do
      echo "gotest.sh: retry $try of packages blocked by Application Control:" $blocked >&2
      # shellcheck disable=SC2086
      "$GO" test "${args[@]}" -ldflags=-X=main.appControlRetry=$try $blocked 2>&1 | tee "$log"
      rc=${PIPESTATUS[0]}
      [ "$rc" -eq 0 ] && exit 0
      next=$(blocked_in "$log")
      # A real failure (anything but a block) ends the retries.
      if grep -E '^(--- FAIL|panic:)' "$log" | grep -q . ||
        [ -n "$(grep -E '^FAIL\t' "$log" | awk '{print $2}' | grep -vxF "${next:-none}")" ]; then
        exit "$rc"
      fi
      [ -n "$next" ] || break # nothing blocked any more: judge this run below
      blocked=$next
    done
    [ -n "$next" ] && exit "$rc"
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
