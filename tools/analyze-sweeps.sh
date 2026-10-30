#!/bin/sh
# analyze-sweeps.sh - run the offline sweep analyzer in the pinned Go container.
#
# Usage:
#   tools/analyze-sweeps.sh <report.json> <capture.raw> [capture.raw...]
#
# Inputs and report must be repository-relative paths composed of ordinary path
# characters. The analyzer is read-only for inputs and writes the report atomically.

set -eu

[ "$#" -ge 2 ] || {
	echo "usage: $0 <report.json> <capture.raw> [capture.raw...]" >&2
	exit 2
}

valid_path() {
	case "$1" in
	*[!A-Za-z0-9_./-]* | '' | /* | *../*) return 1 ;;
	esac
}

for path in "$@"; do
	valid_path "$path" || {
		echo "unsafe or non-repository-relative path: $path" >&2
		exit 2
	}
done

out=$1
shift
# tools/build.sh runs the Go command from yastationd/, one level below the
# repository-root-relative paths accepted by this wrapper.
args="-out ../$out"
for capture in "$@"; do
	[ -f "$capture" ] || {
		echo "capture does not exist: $capture" >&2
		exit 2
	}
	args="$args ../$capture"
done

exec env SWEEP_ANALYZE_ARGS="$args" tools/build.sh sweep-analyze
