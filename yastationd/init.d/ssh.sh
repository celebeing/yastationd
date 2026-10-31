#!/bin/sh
# ssh.sh — the speaker's SSH server: dropbear (the default) or OpenSSH (openssh/, docs/ssh.md).
#
#   ssh.sh start                    the server <cfw>/ssh_server names, on port 22
#   ssh.sh stop                     stop whichever runs on port 22
#   ssh.sh test PORT [SUFFIX]       OpenSSH on PORT beside the live server, binaries <name>SUFFIX
#                                   (deploy.sh tests new ones as .new), pid /var/run/sshd-PORT.pid;
#                                   SERVER=dropbear: dropbear on PORT instead
#   ssh.sh config PORT [SUFFIX]     print the sshd_config it would use
#
# <cfw>/ssh_server holds "openssh" or "dropbear" (missing = dropbear). OpenSSH starts only when
# its files are there and `sshd -t` accepts the config; otherwise dropbear starts, so a broken
# OpenSSH never leaves the speaker without SSH. Both use the same ed25519 host key (OpenSSH's is
# converted from dropbear's once), so clients see the same server either way.
#
# OpenSSH reads, when present (the owner's, never shipped by the CFW):
#   <cfw>/openssh/trusted_user_ca_keys   CA public keys whose user certificates are accepted
#   <cfw>/openssh/principals/<user>      the principals a certificate must name for that user
#                                        (without it a certificate must name the user itself)
#   <cfw>/openssh/sshd_config.local      extra sshd_config lines, read first (they win)
#   <cfw>/authorized_keys                keys added from the Wi-Fi setup page, next to the rootfs's
#                                        /root/.ssh/authorized_keys
CFW=/bkupgrade/cfw
[ -d "$CFW" ] || CFW=/data/cfw
D=${SSH_DIR:-$CFW/openssh} # SSH_DIR: a trial copy elsewhere (docs/ssh.md, "Trying it by hand")
DB=$CFW/bin/dropbearmulti
DBKEY=$CFW/dropbear_ed25519_host_key
KEY=$D/ssh_host_ed25519_key

mode() { cat "$CFW/ssh_server" 2>/dev/null || echo dropbear; }

# hostkey: OpenSSH's host key, converted from dropbear's (made first if the speaker has none).
hostkey() {
	[ -s "$KEY" ] && return 0
	[ -s "$DBKEY" ] || "$DB" dropbearkey -t ed25519 -f "$DBKEY" >/dev/null 2>&1 || return 1
	"$D/dropbearconvert" dropbear openssh "$DBKEY" "$KEY.tmp" >/dev/null 2>&1 || return 1
	chmod 600 "$KEY.tmp" && mv "$KEY.tmp" "$KEY"
}

config() { # port suffix
	echo "# written by $0; changes go to sshd_config.local"
	[ -f "$D/sshd_config.local" ] && echo "Include $D/sshd_config.local"
	cat <<EOF
Port $1
HostKey $KEY
PidFile /var/run/sshd-$1.pid
SshdSessionPath $D/sshd-session$2
SshdAuthPath $D/sshd-auth$2
Subsystem sftp $D/sftp-server$2
PermitRootLogin prohibit-password
PasswordAuthentication no
KbdInteractiveAuthentication no
AuthorizedKeysFile /root/.ssh/authorized_keys $CFW/authorized_keys
UseDNS no
# The only login is root and every file here is root's, but StrictModes would still refuse the
# principals files: on bergamot /bkupgrade belongs to a vendor uid.
StrictModes no
# OpenSSH shuts out an address after failed logins (up to 10 min by default); SSH is our only way
# in, so a failed test or a stray client costs at most a minute.
PerSourcePenalties max:60s
EOF
	[ -s "$D/trusted_user_ca_keys" ] && echo "TrustedUserCAKeys $D/trusted_user_ca_keys"
	[ -d "$D/principals" ] && echo "AuthorizedPrincipalsFile $D/principals/%u"
	return 0
}

# openssh PORT SUFFIX: check the config and start sshd on PORT; fails without starting anything.
openssh() {
	[ -x "$D/sshd$2" ] && [ -x "$D/sshd-session$2" ] && [ -x "$D/sshd-auth$2" ] || return 1
	hostkey || return 1
	mkdir -p /tmp/sshd-empty && chmod 755 /tmp/sshd-empty || return 1
	config "$1" "$2" >"/tmp/sshd_config.$1"
	"$D/sshd$2" -t -f "/tmp/sshd_config.$1" >"/tmp/sshd-$1.log" 2>&1 || return 1
	"$D/sshd$2" -f "/tmp/sshd_config.$1" -E "/tmp/sshd-$1.log"
}

dropbear() {
	[ -x "$DB" ] || return 1
	[ -f "$DBKEY" ] || "$DB" dropbearkey -t ed25519 -f "$DBKEY" >/dev/null 2>&1
	"$DB" dropbear -r "$DBKEY" -p 22 -P /var/run/dropbear.pid >/tmp/dropbear.log 2>&1
}

stop22() {
	for p in /var/run/dropbear.pid /var/run/sshd-22.pid; do
		[ -f "$p" ] && kill "$(cat "$p")" 2>/dev/null
		rm -f "$p"
	done
	return 0
}

case "$1" in
start)
	if [ "$(mode)" = openssh ] && openssh 22 ""; then
		echo "ssh: openssh on 22"
	else
		[ "$(mode)" = openssh ] && echo "ssh: openssh did not start (/tmp/sshd-22.log), dropbear instead" >&2
		dropbear && echo "ssh: dropbear on 22"
	fi
	;;
stop) stop22 ;;
test)
	[ -n "${2:-}" ] || { echo "usage: $0 test PORT [SUFFIX]" >&2; exit 2; }
	if [ "${SERVER:-openssh}" = dropbear ]; then
		"$DB" dropbear -r "$DBKEY" -p "$2" -P "/var/run/sshd-$2.pid" >"/tmp/sshd-$2.log" 2>&1 || exit 1
	else
		openssh "$2" "${3:-}" || { cat "/tmp/sshd-$2.log" >&2; exit 1; }
	fi
	;;
config) config "${2:-22}" "${3:-}" ;;
*)
	echo "usage: $0 start|stop|test PORT [SUFFIX]|config [PORT [SUFFIX]]" >&2
	exit 2
	;;
esac
