#!/bin/sh
# One-time fetch of the OpenSSL and OpenSSH releases into src/ (gitignored), pinned by sha256. The
# pins were taken from tarballs whose release signatures checked out: OpenSSH against Damien
# Miller's release key (7168 B983 815A 5EEF 59A4  ADFD 2A3F 414E 7360 60BA, RELEASE_KEY.asc on
# cdn.openbsd.org), OpenSSL against the project's key (B146 647E 45A7 B339 47AB  226B 2A2C 87D1
# 6169 2D40, openssl-library.org/source/pubkeys.asc). A new pin needs the same check (README.md).
# Re-running is a no-op once fetched; delete src/ to refetch.
set -eu
cd "$(dirname "$0")"
mkdir -p src

OPENSSL=openssl-3.5.9
OPENSSL_SHA256=603f5602e2eef00d77fbd429d34dcd5822bb301757a1bc9cdb24c670f1eb859a
OPENSSH=openssh-10.6p1
OPENSSH_SHA256=a9dc9565dffe8640f64d863cd29a32bc4a3dbdec0566a7fc44c5d6ee767d5f39

get() { # name sha256 url
	[ -e src/$1/.fetched ] && return 0
	curl -fsSL --retry 3 --retry-delay 5 -o src/$1.tar.gz "$3"
	echo "$2  src/$1.tar.gz" | sha256sum -c -
	rm -rf src/$1
	tar -xzf src/$1.tar.gz -C src
	rm src/$1.tar.gz
	touch src/$1/.fetched
	echo "fetched $1"
}
get $OPENSSL $OPENSSL_SHA256 https://github.com/openssl/openssl/releases/download/$OPENSSL/$OPENSSL.tar.gz
get $OPENSSH $OPENSSH_SHA256 https://cdn.openbsd.org/pub/OpenBSD/OpenSSH/portable/$OPENSSH.tar.gz
