#!/bin/sh
# One-time fetch of the dropbear release into src/ (gitignored), pinned by its sha256. The pin was
# taken from a tarball whose release signature checked out against the dropbear release key
# (F7347EF2EE2E07A267628CA944931494F29C6773, "Dropbear SSH Release Signing"); a new pin needs the
# same check (README.md). Re-running is a no-op once fetched; delete src/ to refetch.
set -eu
cd "$(dirname "$0")"
mkdir -p src

DROPBEAR=dropbear-2026.94
DROPBEAR_SHA256=e098034a843699200c8c977a991fff73159735bf795d5f72ef672c41a6b1ae81

if [ ! -e src/$DROPBEAR/.fetched ]; then
	curl -fsSL --retry 3 --retry-delay 5 -o src/$DROPBEAR.tar.bz2 https://matt.ucc.asn.au/dropbear/releases/$DROPBEAR.tar.bz2
	echo "$DROPBEAR_SHA256  src/$DROPBEAR.tar.bz2" | sha256sum -c -
	rm -rf src/$DROPBEAR
	tar -xjf src/$DROPBEAR.tar.bz2 -C src
	rm src/$DROPBEAR.tar.bz2
	touch src/$DROPBEAR/.fetched
	echo "fetched $DROPBEAR"
fi
