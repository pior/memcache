#!/usr/bin/env bash
# Run this module's fuzz targets.
#
#   scripts/fuzz.sh [duration] [target...]
#
# With no target, every target in every package is fuzzed for `duration` each.
# A crash is written to the package's testdata/fuzz/<Target>/ directory, where
# `go test` replays it as an ordinary test from then on: commit it.
set -uo pipefail

DURATION="${1:-30s}"
shift || true
WANTED="$*"

cd "$(dirname "$0")/.."

run_target() {
  go test -run='^$' -fuzz="^${2}\$" -fuzztime="$DURATION" "$1"
}

failed=0
for pkg in . ./meta; do
  for target in $(go test -list='^Fuzz' "$pkg" | grep '^Fuzz'); do
    if [ -n "$WANTED" ] && ! echo " $WANTED " | grep -q " $target "; then
      continue
    fi

    echo "--- fuzzing $target in $pkg for $DURATION"
    if run_target "$pkg" "$target"; then
      continue
    fi

    # The fuzzing runtime sometimes reports "context deadline exceeded" when a
    # worker is still busy as the time limit expires, which is not a finding.
    # Retrying cannot hide a real one: a crash is written to
    # <pkg>/testdata/fuzz/<Target>/ and replayed as a seed before the retry
    # starts fuzzing, so a genuine failure fails again immediately.
    echo "--- $target failed; retrying once to rule out a runtime timeout"
    if run_target "$pkg" "$target"; then
      echo "--- $target passed on retry, treating the first failure as a timeout"
      continue
    fi

    echo "--- $target failed twice"
    failed=1
  done
done

exit "$failed"
