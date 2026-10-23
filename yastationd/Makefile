# yastationd — build, stage and deploy (guide: docs/deploying.md). A CGO-free static armv7 binary
# runs on every supported userland (armhf, and aarch64 kernels with 32-bit EL0 support).

BIN      := yastationd
OUT      := build/$(BIN)
PKG      := ./cmd/yastationd
# Device address and ssh key come from local.mk (gitignored; see local.mk.example).
-include local.mk
DEVICE   ?= root@yastation.local
# CFW root on the device: bergamot /bkupgrade/cfw, pickle /data/cfw (set in local.mk).
DESTDIR  ?= /bkupgrade/cfw

GOFLAGS  := -trimpath -ldflags="-s -w"
GOENV    := CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7

.PHONY: all build test vet clean stage stage-tmp redeploy-tmp install-initd sounds deploy deploy-spotter

all: test build

build:
	$(GOENV) go build $(GOFLAGS) -o $(OUT) $(PKG)
	@file $(OUT)

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf build

# The device has no scp (dropbear without it), so files are streamed over ssh via `cat >`.
SSHKEY   ?= $(HOME)/.ssh/id_yastation
SSH      := ssh -i $(SSHKEY) -o IdentitiesOnly=yes $(DEVICE)

# Copy the binary + the init hook to $(DESTDIR) (persistent, no rootfs write, no autostart).
stage: build
	$(SSH) 'mkdir -p $(DESTDIR)/bin $(DESTDIR)/init.d'
	$(SSH) 'cat > $(DESTDIR)/bin/$(BIN).new' < $(OUT)
	$(SSH) 'mv $(DESTDIR)/bin/$(BIN).new $(DESTDIR)/bin/$(BIN) && chmod +x $(DESTDIR)/bin/$(BIN)'
	$(SSH) 'cat > $(DESTDIR)/init.d/S52zyastationd && chmod +x $(DESTDIR)/init.d/S52zyastationd' < init.d/S52zyastationd
	$(SSH) 'cat > $(DESTDIR)/init.d/supervise.sh.new && mv $(DESTDIR)/init.d/supervise.sh.new $(DESTDIR)/init.d/supervise.sh' < init.d/supervise.sh
	@echo "staged to $(DEVICE):$(DESTDIR) — not yet autostarted (see install-initd)"

# Throwaway copy in tmpfs for manual on-device testing (gone on reboot). Written aside and
# renamed in, so it works while the old binary runs (a supervisor respawn then picks it up).
stage-tmp: build
	$(SSH) 'cat > /tmp/$(BIN).new && chmod +x /tmp/$(BIN).new && mv /tmp/$(BIN).new /tmp/$(BIN)' < $(OUT)

# stage-tmp, then kill the running /tmp binary so its supervisor respawns the new one.
redeploy-tmp: stage-tmp
	$(SSH) 'kill $$(ps w | awk "\$$3==\"/tmp/$(BIN)\" {print \$$1}")'


# Install the init hook (rootfs edit: remount rw/ro around it).
install-initd: stage
	$(SSH) 'mount -o remount,rw / && \
	  cp $(DESTDIR)/init.d/S52zyastationd /etc/init.d/ && \
	  chmod +x /etc/init.d/S52zyastationd && \
	  sync && mount -o remount,ro /'

# Vendor sounds the daemon plays with aplay, which only does wav. The device copies are opus or
# mp3 (bergamot 0.369: ru-RU/no_power.opus; pickle and bergamot 0.6.x: an Ogg/Opus file *named*
# .wav; the volume tick: common/volume_sound.mp3, or sounds/volume_sound.mp3 on 0.6.x), and
# there's no decoder on board: pull, decode on the host, put a real wav under $(DESTDIR)/sounds/.
# Vendor audio, so it's never stored in the repo. A failed decode leaves the old file alone.
SOUNDS ?= no_power volume_sound
sounds:
	$(SSH) 'mkdir -p $(DESTDIR)/sounds'
	set -e; for s in $(SOUNDS); do \
	  $(SSH) "d=/system/vendor/quasar/sounds; for f in ru-RU/$$s.opus ru-RU/$$s.wav common/$$s.mp3 $$s.mp3; do [ -f \$$d/\$$f ] && exec cat \$$d/\$$f; done; exit 1" > build/$$s.src; \
	  ffmpeg -v error -y -i build/$$s.src -ar 48000 -ac 2 build/$$s.wav; \
	  $(SSH) "cat > $(DESTDIR)/sounds/$$s.wav.new && mv $(DESTDIR)/sounds/$$s.wav.new $(DESTDIR)/sounds/$$s.wav" < build/$$s.wav; \
	  rm -f build/$$s.src build/$$s.wav; \
	done

# Upgrade a running install in place: upload aside, then stop -> swap -> start. The swap runs as a
# script on stdin (`sh -s`) so no process command line contains the binary's path — the init
# hook's stop matches processes by path, and an ssh command mentioning it would be killed with them.
# The running binary is kept as .prev: the supervisor swaps it back in if the new one crash-loops.
deploy: build
	$(SSH) 'cat > $(DESTDIR)/bin/$(BIN).upload' < $(OUT)
	$(SSH) 'mkdir -p $(DESTDIR)/init.d && cat > $(DESTDIR)/init.d/supervise.sh.new && mv $(DESTDIR)/init.d/supervise.sh.new $(DESTDIR)/init.d/supervise.sh' < init.d/supervise.sh
	printf '%s\n' 'set -e' 'D=$(DESTDIR)' 'chmod +x $$D/bin/$(BIN).upload' \
	  '/etc/init.d/S52zyastationd stop >/dev/null 2>&1 || true' 'sleep 1' \
	  '[ -f $$D/bin/$(BIN) ] && cp -p $$D/bin/$(BIN) $$D/bin/$(BIN).prev' \
	  'mv $$D/bin/$(BIN).upload $$D/bin/$(BIN)' '/etc/init.d/S52zyastationd start' \
	  'sleep 8' 'tail -5 /tmp/yastationd.log' | $(SSH) 'sh -s'

# Install a spotter-host build (spotter-host/build.sh, ARCH matching the device's userland). The
# running child keeps the old file; it takes effect when yastationd next starts it — restart the
# daemon (make deploy, or the init hook's restart).
SPOTTER ?= ../spotter-host/spotter-host
deploy-spotter:
	$(SSH) 'cat > $(DESTDIR)/bin/spotter-host.upload && chmod +x $(DESTDIR)/bin/spotter-host.upload && mv $(DESTDIR)/bin/spotter-host.upload $(DESTDIR)/bin/spotter-host' < $(SPOTTER)
