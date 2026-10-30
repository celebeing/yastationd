#!/bin/sh
# check-docs.sh — every relative link in the repo's Markdown points at something that exists.
#
#   tools/check-docs.sh            (also: tools/build.sh docs-check)
#
# Checks [text](path) links in tracked *.md files (and the docs' untracked new files) that are
# not URLs: the path, relative to the file, must exist (a file or a directory) and not be
# git-ignored (a local-only file would be a broken link for everyone else); an anchor into a
# Markdown file (`doc.md#a-heading`, `#a-heading`) must match one of its headings, slugged as
# GitHub does (lower case; punctuation dropped; spaces to '-'). Without a UTF-8 locale, anchors
# with non-ASCII letters are skipped. Exit 1 with one line per broken link. Needs only sh, git,
# grep, sed.
set -u
cd "$(dirname "$0")/.." || exit 2
for l in C.UTF-8 en_US.UTF-8; do
	if printf '\320\226\n' | LC_ALL=$l grep -qi "$(printf '\320\266')" 2>/dev/null; then export LC_ALL=$l; break; fi
done
utf8=0
printf '\320\226\n' | grep -qi "$(printf '\320\266')" 2>/dev/null && utf8=1
# slugs FILE: the anchors of FILE's headings, one per line (a repeated heading also gets -1, -2…
# on GitHub; those are accepted by stripping the suffix before the lookup).
slugs() {
	grep '^#\{1,6\} ' "$1" | sed -e 's/^#* *//' -e 's/ *#* *$//' -e 's/[^[:alnum:] _-]//g' -e 's/ /-/g'
}
bad=0
files=$( { git ls-files '*.md'; git ls-files --others --exclude-standard '*.md'; } | grep -v -e node_modules -e '^dumps/' | sort -u)
for f in $files; do
	[ -f "$f" ] || continue
	dir=$(dirname "$f")
	# [text](target) → target; drop the "title" part and the anchor
	grep -o '\]([^)]*)' "$f" | sed -e 's/^](//' -e 's/)$//' -e 's/ .*//' | while read -r t; do
		case $t in '' | *://* | mailto:* | tel:*) continue ;; esac
		anchor=
		case $t in *'#'*) anchor=${t#*#}; t=${t%%#*} ;; esac
		if [ -n "$anchor" ]; then
			doc=$dir/$t
			[ -z "$t" ] && doc=$f
			case $doc in *.md) ;; *) doc= ;; esac
			if [ -n "$doc" ] && [ -f "$doc" ]; then
				if [ $utf8 -eq 0 ] && printf '%s' "$anchor" | LC_ALL=C grep -q '[^ -~]'; then
					: # can't fold non-ASCII case here
				elif ! slugs "$doc" | grep -qixF -e "$anchor" -e "$(printf '%s' "$anchor" | sed 's/-[0-9]*$//')"; then
					echo "$f: no such heading: ${t:-(this file)}#$anchor"; echo x >&3
				fi
			fi
		fi
		[ -z "$t" ] && continue
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
