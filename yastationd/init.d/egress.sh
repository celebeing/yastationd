#!/bin/sh
# LAN-safe egress control for the station (de-Yandexed box). Lives at <cfw>/egress.sh; the boot hook
# /etc/init.d/S99zzegress runs `lock` after the vendor's own iptables init. docs/deploying.md §0.
#   lock   = only LAN/loopback/established/DHCP/multicast out; public internet DROPPED (IPv4 and IPv6)
#   open   = remove the lock (full egress) — TEMPORARY, for downloading packages/code
#   status = show state + counters
# dropbear :22 / adb are INBOUND (INPUT) + ESTABLISHED, so lock never cuts our access. The IPv6 part
# is independent of the IPv4 one: whatever happens to it, the IPv4 rules (our access path) are built
# first and the same as before.
# iptables lives in /usr/sbin, which isn't in PATH under init.d / non-login shells
# (S99zzegress + plain `ssh host cmd`) — without this, lock silently no-ops.
export PATH=/usr/sbin:/sbin:/usr/bin:/bin:$PATH

# iptables resolution. Prefer a system iptables; else fall back to the armhf bundle the install kit
# ships at $BUNDLE (official Debian-buster iptables-legacy + its libs + xtables extension .so dir).
# bergamot fw 0.6.1.4 ships NO iptables at all, but its kernel has full netfilter built-in, so the
# bundle drives it fine; the bundle's xtables-legacy-multi is also its ip6tables.
# $IPT / $IPT6 may be multi-word (env ...), so they are used UNQUOTED on purpose below.
CFWR=/bkupgrade/cfw; [ -d "$CFWR" ] || CFWR=/data/cfw
BUNDLE=$CFWR/iptables
BENV="env LD_LIBRARY_PATH=$BUNDLE/lib XTABLES_LIBDIR=$BUNDLE/xtables"
if command -v iptables >/dev/null 2>&1; then
  IPT="iptables"
elif [ -x "$BUNDLE/bin/iptables" ]; then
  IPT="$BENV $BUNDLE/bin/iptables"
else
  echo "egress.sh: no iptables (system or bundled $BUNDLE) — on-device egress UNAVAILABLE (rely on router MAC-drop + maind-off)" >&2
  exit 2
fi
if command -v ip6tables >/dev/null 2>&1; then
  IPT6="ip6tables"
elif [ -x "$BUNDLE/bin/xtables-legacy-multi" ]; then
  IPT6="$BENV $BUNDLE/bin/xtables-legacy-multi ip6tables"
else
  IPT6=""
fi

CH=CFW_EGRESS
build() {
  $IPT -N $CH 2>/dev/null
  $IPT -F $CH
  $IPT -A $CH -o lo -j ACCEPT
  $IPT -A $CH -m state --state ESTABLISHED,RELATED -j ACCEPT
  $IPT -A $CH -d 127.0.0.0/8 -j ACCEPT
  $IPT -A $CH -d 10.0.0.0/8 -j ACCEPT
  $IPT -A $CH -d 172.16.0.0/12 -j ACCEPT
  $IPT -A $CH -d 192.168.0.0/16 -j ACCEPT
  $IPT -A $CH -d 169.254.0.0/16 -j ACCEPT
  $IPT -A $CH -d 224.0.0.0/4 -j ACCEPT
  $IPT -A $CH -d 255.255.255.255/32 -j ACCEPT
  $IPT -A $CH -p udp --dport 67:68 -j ACCEPT
  $IPT -A $CH -j DROP
}
hook()   { $IPT -C OUTPUT -j $CH >/dev/null 2>&1 || $IPT -I OUTPUT 1 -j $CH; }
unhook() { while $IPT -C OUTPUT -j $CH >/dev/null 2>&1; do $IPT -D OUTPUT -j $CH; done; }

# IPv6, the same policy: loopback, link-local (fe80::/10), unique-local (fc00::/7, the IPv6 LAN
# ranges), multicast (ff00::/8: neighbour discovery, mDNS, DHCPv6 to ff02::1:2) and established
# flows pass; global addresses are dropped. Without any ip6tables, IPv6 is switched off on every
# interface but lo instead (nothing of ours uses IPv6; ::1 stays for the local resolver).
CH6=CFW_EGRESS6
build6() {
  $IPT6 -N $CH6 2>/dev/null
  $IPT6 -F $CH6
  $IPT6 -A $CH6 -o lo -j ACCEPT
  $IPT6 -A $CH6 -m state --state ESTABLISHED,RELATED -j ACCEPT
  $IPT6 -A $CH6 -d fe80::/10 -j ACCEPT
  $IPT6 -A $CH6 -d fc00::/7 -j ACCEPT
  $IPT6 -A $CH6 -d ff00::/8 -j ACCEPT
  $IPT6 -A $CH6 -j DROP
}
hook6()   { $IPT6 -C OUTPUT -j $CH6 >/dev/null 2>&1 || $IPT6 -I OUTPUT 1 -j $CH6; }
unhook6() { while $IPT6 -C OUTPUT -j $CH6 >/dev/null 2>&1; do $IPT6 -D OUTPUT -j $CH6; done; }
v6off() { # $1 = 1 (off) or 0 (back on). Not via conf/all: the kernel copies that onto lo too.
  for c in /proc/sys/net/ipv6/conf/*; do
    case "${c##*/}" in lo | all) continue ;; esac
    [ -w "$c/disable_ipv6" ] && echo "$1" > "$c/disable_ipv6"
  done
}
lock6() {
  if [ -n "$IPT6" ] && build6 && hook6; then
    echo "IPv6 egress locked (LAN-only)"
  else
    v6off 1
    echo "IPv6: no ip6tables — IPv6 switched off on all interfaces but lo"
  fi
}
open6() {
  [ -n "$IPT6" ] && unhook6
  [ -n "$IPT6" ] || v6off 0
}
status6() {
  if [ -n "$IPT6" ] && $IPT6 -C OUTPUT -j $CH6 >/dev/null 2>&1; then
    echo "ipv6: LOCKED"
    $IPT6 -L $CH6 -nv 2>/dev/null | tail -1
  elif [ "$(cat /proc/sys/net/ipv6/conf/default/disable_ipv6 2>/dev/null)" = 1 ]; then
    echo "ipv6: OFF (disabled on interfaces)"
  else
    echo "ipv6: OPEN"
  fi
}

case "$1" in
  lock)   build; hook; echo "EGRESS LOCKED (LAN-only; public internet blocked)"; lock6;;
  open)   unhook; open6; echo "EGRESS OPEN (full internet) — remember to re-lock";;
  status) if $IPT -C OUTPUT -j $CH >/dev/null 2>&1; then echo "state: LOCKED"; else echo "state: OPEN"; fi
          $IPT -L $CH -nv 2>/dev/null | tail -2
          status6;;
  *) echo "usage: egress.sh {lock|open|status}";;
esac
