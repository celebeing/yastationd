# yastationd — build & stage. The device is armv7 hard-float userland on a 64-bit kernel;
# a CGO-free static binary sidesteps the glibc-2.30 coupling (see docs/yastationd-design.md §1).

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

.PHONY: all build test vet clean stage stage-tmp redeploy-tmp install-initd sounds

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

# Vendor voice lines the daemon plays with aplay, which only does wav. The device copies are opus
# (bergamot 0.369: no_power.opus; pickle and bergamot 0.6.x: an Ogg/Opus file *named* .wav), and
# there's no decoder on board: pull, decode on the host, put a real wav under $(DESTDIR)/sounds/.
# Vendor audio, so it's never stored in the repo. A failed decode leaves the old file alone.
SOUNDS ?= no_power
sounds:
	$(SSH) 'mkdir -p $(DESTDIR)/sounds'
	set -e; for s in $(SOUNDS); do \
	  $(SSH) "d=/system/vendor/quasar/sounds/ru-RU; cat \$$d/$$s.opus 2>/dev/null || cat \$$d/$$s.wav" > build/$$s.src; \
	  ffmpeg -v error -y -i build/$$s.src -ar 48000 -ac 2 build/$$s.wav; \
	  $(SSH) "cat > $(DESTDIR)/sounds/$$s.wav.new && mv $(DESTDIR)/sounds/$$s.wav.new $(DESTDIR)/sounds/$$s.wav" < build/$$s.wav; \
	  rm -f build/$$s.src build/$$s.wav; \
	done
