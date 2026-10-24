#!/bin/sh
# Boots an emulated speaker under qemu-system-aarch64 -M virt (see emu/README.md).
#
#   emu/run.sh <board> [--maint [--auto]] [--fresh] [--ssh-port N] [--headless] [--log FILE]
#                      [--fwd HOSTPORT:GUESTPORT]... [--broker HOSTPORT]
#
#   --maint         boot the maintenance initramfs (the emulator's "UART": rootfs and rw
#                   partition mounted rw, seed share, provision.sh) instead of the vendor init
#   --auto          with --maint: run provision.sh, sync, power off (no shell)
#   --fresh         recreate the flash images from the dumps (wipes the rw partition)
#   --ssh-port N    host port forwarded to the guest's :22 on 127.0.0.1 (default 2222/2223)
#   --headless      no console: serial goes to the log, QEMU runs in the background
#                   (with --maint --auto it still waits for the power-off)
#   --log FILE      also write the serial console to FILE (default with --headless:
#                   emu/work/<board>/serial.log)
#   --fwd H:G       extra host 127.0.0.1:H -> guest :G forward (e.g. 10700 for Wyoming)
#   --broker H      the guest's 10.0.2.100:1883 reaches a test broker on host 127.0.0.1:H
#   --dry-run       print the QEMU command line and exit (no images, no QEMU)
#
# Networking is QEMU user mode with restrict=on: the guest reaches neither the internet nor the
# LAN, only the forwards above. That is a hard requirement (vendor code, and an emulated twin
# must never talk to the real broker or HA): do not remove it.
#
# The last normal-boot invocation is saved to emu/work/<board>/last-run.args for powercut.sh.
set -eu
. "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

[ $# -ge 1 ] || { sed -n '2,25p' "$0"; exit 1; }
load_board "$1"; shift
ORIG_ARGS="$*"

MAINT=0 AUTO=0 FRESH=0 HEADLESS=0 LOG= SSH_PORT= FWDS= BROKER= DRY=0
while [ $# -gt 0 ]; do
	case $1 in
		--maint) MAINT=1 ;;
		--auto) AUTO=1 ;;
		--fresh) FRESH=1 ;;
		--headless) HEADLESS=1 ;;
		--log) LOG=$2; shift ;;
		--ssh-port) SSH_PORT=$2; shift ;;
		--fwd) FWDS="$FWDS $2"; shift ;;
		--broker) BROKER=$2; shift ;;
		--dry-run) DRY=1 ;;
		*) die "unknown option $1" ;;
	esac
	shift
done
[ "$AUTO" = 0 ] || [ "$MAINT" = 1 ] || die "--auto needs --maint"
if [ -z "$SSH_PORT" ]; then
	case $BOARD in bergamot) SSH_PORT=2222 ;; pickle) SSH_PORT=2223 ;; *) SSH_PORT=2224 ;; esac
fi

[ "$DRY" = 1 ] || command -v "$QEMU" >/dev/null || die "$QEMU not installed (apt install qemu-system-arm)"
[ "$DRY" = 1 ] || [ -f "$KERNEL" ] || die "kernel $KERNEL missing (build it: emu/kernel/)"
if [ "$DRY" = 0 ] && [ "$MAINT" = 1 ] && [ ! -f "$INITRD" ]; then
	note "building the maintenance initramfs"
	sh "$EMU/maint/build.sh" >&2
fi

if p=$(qemu_pid); then die "$BOARD is already running (pid $p); stop it or use emu/powercut.sh"; fi

mkdir -p "$BDIR" "$RESULTS"
if [ "$DRY" = 0 ] && { [ "$FRESH" = 1 ] || ! images_present; }; then
	make_images
fi

# --- kernel cmdline ---
CMDLINE="console=ttyAMA0 $(board_args)$(mtd_args) $ROOT_ARGS ${KERNEL_EXTRA:-}"
if [ "$MAINT" = 1 ]; then
	# rdinit runs our initramfs; root= stays for reference but is unused.
	CMDLINE="$CMDLINE rdinit=/init emu.board=$BOARD emu.rootvol=$MAINT_ROOTVOL"
	CMDLINE="$CMDLINE emu.rwvol=$MAINT_RWVOL emu.rwpath=$RW_PATH panic=5"
	[ "$AUTO" = 1 ] && CMDLINE="$CMDLINE emu.auto=1"
else
	CMDLINE="$CMDLINE ip=dhcp panic=5"
fi

# --- network: restricted user-mode only ---
NET="user,id=net0,restrict=on,hostfwd=tcp:127.0.0.1:$SSH_PORT-:22"
for f in $FWDS; do
	NET="$NET,hostfwd=tcp:127.0.0.1:${f%%:*}-:${f#*:}"
done
if [ -n "$BROKER" ]; then
	NET="$NET,guestfwd=tcp:10.0.2.100:1883-tcp:127.0.0.1:$BROKER"
fi
case $NET in *restrict=on*) ;; *) die "internal: network must be restricted" ;; esac

# --- serial / console ---
if [ "$HEADLESS" = 1 ] && [ -z "$LOG" ]; then LOG=$BDIR/serial.log; fi
set -- -M virt -cpu cortex-a53 -smp "$SMP" -m "$RAM" \
	-kernel "$KERNEL" -append "$CMDLINE" \
	-netdev "$NET" -device virtio-net-pci,netdev=net0 \
	-device virtio-rng-pci \
	-device i6300esb \
	-pidfile "$BDIR/qemu.pid" \
	-qmp "unix:$BDIR/qmp.sock,server=on,wait=off" \
	-display none
# shellcheck disable=SC2046
set -- "$@" $(disk_qemu_args)
if [ "$MAINT" = 1 ]; then
	mkdir -p "$SEED"
	set -- "$@" -initrd "$INITRD" \
		-virtfs "local,path=$SEED,mount_tag=seed,security_model=none,id=seed"
fi

if [ "$HEADLESS" = 1 ]; then
	[ "$DRY" = 1 ] || : > "$LOG"
	set -- "$@" -serial "file:$LOG" -monitor none
	if [ "$AUTO" = 0 ]; then
		set -- "$@" -daemonize
	fi
elif [ -n "$LOG" ]; then
	set -- "$@" -chardev "stdio,id=con0,mux=on,signal=off,logfile=$LOG" \
		-serial chardev:con0 -mon chardev=con0
else
	set -- "$@" -chardev stdio,id=con0,mux=on,signal=off -serial chardev:con0 -mon chardev=con0
fi

if [ "$DRY" = 1 ]; then
	printf '%s' "$QEMU"; for a in "$@"; do printf " '%s'" "$a"; done; echo
	exit 0
fi
if [ "$MAINT" = 0 ]; then
	# Saved for powercut.sh: same board and options, never --fresh.
	echo "$ORIG_ARGS" | sed 's/--fresh//g' > "$BDIR/last-run.args"
fi
note "$BOARD: $([ "$MAINT" = 1 ] && echo maintenance || echo normal) boot, ssh -p $SSH_PORT root@127.0.0.1${LOG:+, serial log $LOG}"
note "cmdline: $CMDLINE"
[ "$HEADLESS" = 1 ] || note "console: Ctrl-A x quits QEMU, Ctrl-A c toggles the monitor"
rm -f "$BDIR/qmp.sock"
if [ "$HEADLESS" = 1 ] && [ "$AUTO" = 0 ]; then
	"$QEMU" "$@"
	note "running in the background, pid $(cat "$BDIR/qemu.pid")"
else
	exec "$QEMU" "$@"
fi
