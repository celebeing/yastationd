#!/bin/sh
# check-docs.sh — every relative link in the repo's Markdown points at something that exists.
#
#   tools/check-docs.sh            (also: tools/build.sh docs-check)
#
# Checks [text](path) links in tracked *.md files (and the docs' untracked new files) that are
# not URLs or pure anchors: the path, relative to the file, must exist (a file or a directory)
# and not be git-ignored (a local-only file would be a broken link for everyone else).
# Anchors are not checked. Exit 1 with one line per broken link. Needs only sh, git, grep, sed.
set -u
cd "$(dirname "$0")/.." || exit 2
bad=0
files=$( { git ls-files '*.md'; git ls-files --others --exclude-standard '*.md'; } | grep -v -e node_modules -e '^dumps/' | sort -u)
for f in $files; do
	[ -f "$f" ] || continue
	dir=$(dirname "$f")
	# [text](target) → target; drop the "title" part and the anchor
	grep -o '\]([^)]*)' "$f" | sed -e 's/^](//' -e 's/)$//' -e 's/ .*//' -e 's/#.*//' | while read -r t; do
		case $t in '' | *://* | mailto:* | tel:*) continue ;; esac
		if [ ! -e "$dir/$t" ]; then
			echo "$f: broken link: $t"; echo x >&3
		elif git check-ignore -q "$dir/$t"; then # here, but not in the repo: broken on GitHub
			echo "$f: link to a git-ignored file: $t"; echo x >&3
		fi
	done 3>>"${TMPDIR:-/tmp}/check-docs.$$"
done
if [ -s "${TMPDIR:-/tmp}/check-docs.$$" ]; then bad=1; fi
rm -f "${TMPDIR:-/tmp}/check-docs.$$"
[ $bad -eq 0 ] && echo "docs: every relative link resolves"
exit $bad
