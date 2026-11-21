#!/usr/bin/env bash
# Checks the TLA+ models under formal/. See formal/README.md.
#
#   formal/tlc.sh [check] [model-dir...]   check every model (default: all)
#   formal/tlc.sh translate [model-dir...] regenerate the PlusCal translations
#
# For each model directory, check
#   1. verifies the committed PlusCal translation is current;
#   2. runs TLC on <Spec>.cfg, which must find no error;
#   3. runs TLC on every <Spec>.mutant-<name>.cfg, which must report the
#      violation named on its first line ("\* expect: <Invariant>",
#      "\* expect: liveness" or "\* expect: deadlock"). A mutant that checks
#      clean means the model cannot see the bug it seeds.
set -euo pipefail

TLA2TOOLS_VERSION=1.7.4
# Computed from the GitHub release asset on 2026-11-20: the release publishes
# no checksum of its own.
TLA2TOOLS_SHA256=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88
TLA2TOOLS_URL="https://github.com/tlaplus/tlaplus/releases/download/v${TLA2TOOLS_VERSION}/tla2tools.jar"

formal_dir=$(cd "$(dirname "$0")" && pwd)

die() {
	echo "tlc.sh: $*" >&2
	exit 1
}

find_java() {
	local candidate
	for candidate in \
		${JAVA_HOME:+"$JAVA_HOME/bin/java"} \
		/opt/homebrew/opt/openjdk/bin/java \
		/usr/local/opt/openjdk/bin/java \
		java; do
		# The macOS /usr/bin/java stub exists but fails without a JDK.
		if "$candidate" -version >/dev/null 2>&1; then
			echo "$candidate"
			return
		fi
	done
	die "no working Java runtime found (set JAVA_HOME, or run: bud up)"
}

sha256() {
	if command -v sha256sum >/dev/null; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

find_jar() {
	if [[ -n "${TLA2TOOLS_JAR:-}" ]]; then
		echo "$TLA2TOOLS_JAR"
		return
	fi
	local dir="${XDG_CACHE_HOME:-$HOME/.cache}/tla2tools/${TLA2TOOLS_VERSION}"
	local jar="$dir/tla2tools.jar"
	if [[ ! -f "$jar" ]]; then
		mkdir -p "$dir"
		echo "Downloading tla2tools ${TLA2TOOLS_VERSION}" >&2
		curl -sSfL -o "$jar.tmp" "$TLA2TOOLS_URL"
		mv "$jar.tmp" "$jar"
	fi
	local actual
	actual=$(sha256 "$jar")
	if [[ "$actual" != "$TLA2TOOLS_SHA256" ]]; then
		rm -f "$jar"
		die "checksum mismatch for tla2tools.jar: got $actual"
	fi
	echo "$jar"
}

model_dirs() {
	if (($# > 0)); then
		printf '%s\n' "$@"
	else
		find "$formal_dir" -mindepth 1 -maxdepth 1 -type d | sort
	fi
}

pluscal_specs() {
	grep -l -- '--\(fair \)\?algorithm' "$1"/*.tla 2>/dev/null || true
}

translate() {
	local spec=$1
	(cd "$(dirname "$spec")" && "$java" -cp "$jar" pcal.trans -nocfg "$(basename "$spec")" </dev/null >/dev/null) ||
		die "PlusCal translation failed: $spec"
	rm -f "${spec%.tla}.old"
}

cmd_translate() {
	local dir spec
	while read -r dir; do
		for spec in $(pluscal_specs "$dir"); do
			translate "$spec"
			echo "translated ${spec#"$formal_dir"/}"
		done
	done < <(model_dirs "$@")
}

failures=0
fail() {
	echo "FAIL  $*"
	failures=$((failures + 1))
}

# run_tlc <cfg> <log>: prints TLC's exit status.
run_tlc() {
	local cfg=$1 log=$2
	local base
	base=$(basename "$cfg")
	local spec="${base%%.*}.tla"
	local status=0
	(cd "$(dirname "$cfg")" &&
		"$java" -XX:+UseParallelGC -cp "$jar" tlc2.TLC \
			-workers auto -cleanup -metadir "$work/meta/$base" \
			-config "$base" "$spec" </dev/null >"$log" 2>&1) || status=$?
	echo "$status"
}

check_model() {
	local dir=$1 spec cfg log status expect name

	for spec in $(pluscal_specs "$dir"); do
		cp "$spec" "$work/$(basename "$spec")"
		translate "$work/$(basename "$spec")"
		if ! cmp -s "$spec" "$work/$(basename "$spec")"; then
			fail "${spec#"$formal_dir"/}: stale PlusCal translation (run: formal/tlc.sh translate)"
		fi
	done

	for cfg in "$dir"/*.cfg; do
		[[ -e "$cfg" ]] || continue
		name=${cfg#"$formal_dir"/}
		log="$work/$(basename "$cfg").log"
		status=$(run_tlc "$cfg" "$log")

		if [[ "$cfg" != *.mutant-*.cfg ]]; then
			if [[ "$status" == 0 ]]; then
				echo "ok    $name ($(grep -o '[0-9,]* distinct states' "$log" | tail -1))"
			else
				fail "$name: TLC exit $status"
				cat "$log"
			fi
			continue
		fi

		expect=$(sed -n '1s/^\\\* expect: *//p' "$cfg")
		[[ -n "$expect" ]] || die "$name: first line must be '\\* expect: <what>'"
		local pattern
		case "$expect" in
		liveness) pattern='Temporal properties were violated' ;;
		deadlock) pattern='Deadlock reached' ;;
		*) pattern="Invariant $expect is violated" ;;
		esac
		if grep -q "$pattern" "$log"; then
			echo "ok    $name (killed: $expect)"
		else
			fail "$name: expected '$pattern', TLC exit $status"
			cat "$log"
		fi
	done
}

cmd_check() {
	local dir
	while read -r dir; do
		check_model "$dir"
	done < <(model_dirs "$@")
	if ((failures > 0)); then
		die "$failures failure(s)"
	fi
}

java=$(find_java)
jar=$(find_jar)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

case "${1:-check}" in
check)
	shift || true
	cmd_check "$@"
	;;
translate)
	shift
	cmd_translate "$@"
	;;
*)
	cmd_check "$@"
	;;
esac
