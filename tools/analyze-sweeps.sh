#!/bin/sh
# analyze-sweeps.sh - run the offline sweep analyzer (cmd/sweep-analyze) on the host.
#
# Usage:
#   tools/analyze-sweeps.sh <report.json> <capture.raw> [capture.raw...]
#
# Builds the analyzer for this host in the pinned Go container (tools/build.sh
# hosttool:sweep-analyze), then runs it. It reads the captures and writes the
# report atomically; it never contacts a speaker.

set -eu

[ "$#" -ge 2 ] || {
	echo "usage: $0 <report.json> <capture.raw> [capture.raw...]" >&2
	exit 2
}
here=$(cd "$(dirname "$0")/.." && pwd)
out=$1
shift
for capture in "$@"; do
	[ -f "$capture" ] || {
		echo "capture does not exist: $capture" >&2
		exit 2
	}
done
"$here/tools/build.sh" hosttool:sweep-analyze >&2
exec "$here/yastationd/build/sweep-analyze" -out "$out" "$@"
