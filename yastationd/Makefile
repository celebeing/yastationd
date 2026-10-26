# yastationd — build, stage and deploy (guide: docs/deploying.md). A CGO-free static armv7 binary
# runs on every supported userland (armhf, and aarch64 kernels with 32-bit EL0 support).
BIN      := yastationd
OUT      := build/$(BIN)
PKG      := ./cmd/yastationd
RINGDEMOBIN := build/ringdemo
FBLEDDEMOBIN := build/fbleddemo
# Device address, SSH key and platform come from local.mk (gitignored; see local.mk.example).
-include local.mk
DEVICE   ?= root@yastation.local
PLATFORM ?= bergamot

# Deployment defaults come from the selected board profile; command-line/local.mk overrides
# remain authoritative. Only the two platform keys are read: a profile is the daemon's config
# format, not a makefile (a `$`, a leading tab or a Make keyword in it must not break the build).
PLATFORM_PROFILE := internal/board/profiles/$(PLATFORM).conf
ifeq ($(wildcard $(PLATFORM_PROFILE)),)
$(error unknown PLATFORM "$(PLATFORM)" (no $(PLATFORM_PROFILE)))
endif
profile_key = $(shell sed -n 's/^[[:space:]]*$(1)[[:space:]]*=[[:space:]]*//p' $(PLATFORM_PROFILE) | sed 's/[[:space:]]*$$//' | head -1)
DESTDIR     ?= $(call profile_key,platform\.cfw_root)
HELPER_ARCH ?= $(call profile_key,platform\.userland_arch)

# The version comes from the latest v* tag (docs/releasing.md); see internal/version.
VERSION  := $(shell git describe --tags --match 'v*' --dirty --always 2>/dev/null || echo dev)
GOFLAGS  := -trimpath -ldflags="-s -w -X github.com/celebeing/yastationd/yastationd/internal/version.Version=$(VERSION)"
GOENV    := CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7

.PHONY: web webui-dist all platform-info build build-ringdemo build-fbleddemo test vet clean stage stage-tmp stage-ringdemo stage-fbleddemo redeploy-tmp install-initd sounds vqe-models deploy deploy-spotter deploy-sendspin clips deploy-egress ota-block video video-convert


all: test build

platform-info:
	@printf 'platform=%s cfw_root=%s userland_arch=%s\n' '$(PLATFORM)' '$(DESTDIR)' '$(HELPER_ARCH)'

# The web app (../web), built into internal/webui/dist for go:embed in the pinned builder container
# (../tools/build-web.sh; docs/web-ui.md). Not committed: `make build` rebuilds it whenever its
# source changed. Plain `go build`/`go test` work without it (the daemon then serves a "not built"
# page instead of the app).
WEB_SRC := $(shell find ../web/src -type f) $(wildcard ../web/index.html ../web/package.json ../web/pnpm-lock.yaml ../web/vite.config.ts ../web/tsconfig.json)
webui-dist: internal/webui/dist/app/index.html
internal/webui/dist/app/index.html: $(WEB_SRC)
	../tools/build-web.sh
web:
	../tools/build-web.sh

build: webui-dist
	$(GOENV) go build $(GOFLAGS) -o $(OUT) $(PKG)
	@file $(OUT)
	@echo "version $(VERSION)"

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf build

# The device has no scp (dropbear without it), so files are streamed over ssh via `cat >`.
# SSHKEY: optional; empty, ssh picks the key from the agent and ~/.ssh/config.
SSHKEY   ?=
SSH      := ssh $(if $(SSHKEY),-i $(SSHKEY) -o IdentitiesOnly=yes) $(DEVICE)

# Copy the binary + the init hook to $(DESTDIR) (persistent, no rootfs write, no autostart).
stage: build
	$(SSH) 'mkdir -p $(DESTDIR)/bin $(DESTDIR)/init.d'
	$(SSH) 'cat > $(DESTDIR)/bin/$(BIN).new' < $(OUT)
	$(SSH) 'mv $(DESTDIR)/bin/$(BIN).new $(DESTDIR)/bin/$(BIN) && chmod +x $(DESTDIR)/bin/$(BIN)'
	$(SSH) 'cat > $(DESTDIR)/init.d/S52zyastationd && chmod +x $(DESTDIR)/init.d/S52zyastationd' < init.d/S52zyastationd
	$(SSH) 'cat > $(DESTDIR)/init.d/supervise.sh.new && mv $(DESTDIR)/init.d/supervise.sh.new $(DESTDIR)/init.d/supervise.sh' < init.d/supervise.sh
	@echo "staged to $(DEVICE):$(DESTDIR) — not yet autostarted (see install-initd)"

# LED dev tools are not part of deploy/stage. Build and ship them to /tmp on demand for
# on-device verification; stop yastationd first so two processes do not write the same LEDs.
build-ringdemo:
	$(GOENV) go build $(GOFLAGS) -o $(RINGDEMOBIN) ./cmd/ringdemo
	@file $(RINGDEMOBIN)

stage-ringdemo: build-ringdemo
	$(SSH) 'cat > /tmp/ringdemo.new && chmod +x /tmp/ringdemo.new && mv /tmp/ringdemo.new /tmp/ringdemo' < $(RINGDEMOBIN)
	@echo "ring effects: ssh $(DEVICE) '/tmp/ringdemo -board auto'"

build-fbleddemo:
	$(GOENV) go build $(GOFLAGS) -o $(FBLEDDEMOBIN) ./cmd/fbleddemo
	@file $(FBLEDDEMOBIN)

stage-fbleddemo: build-fbleddemo
	$(SSH) 'cat > /tmp/fbleddemo.new && chmod +x /tmp/fbleddemo.new && mv /tmp/fbleddemo.new /tmp/fbleddemo' < $(FBLEDDEMOBIN)
	@echo "screen smoke: ssh $(DEVICE) '/tmp/fbleddemo -clock'  (or -uwu)"

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

# Install the egress lock (init.d/egress.sh -> $(DESTDIR)/egress.sh, IPv4 + IPv6) and apply it. The
# running copy is kept as egress.sh.prev; the new one is first run read-only (status). The boot hook
# /etc/init.d/S99zzegress that runs `lock` comes with the install kit (docs/deploying.md §0).
deploy-egress:
	$(SSH) 'cat > $(DESTDIR)/egress.sh.new' < init.d/egress.sh
	printf '%s\n' 'set -e' 'D=$(DESTDIR)' 'sh $$D/egress.sh.new status >/dev/null' \
	  '[ -f $$D/egress.sh ] && cp -p $$D/egress.sh $$D/egress.sh.prev' \
	  'chmod +x $$D/egress.sh.new && mv $$D/egress.sh.new $$D/egress.sh' \
	  'sh $$D/egress.sh lock' 'sh $$D/egress.sh status' | $(SSH) 'sh -s'

# Third update guard: replace the OTA signing key with init.d/ota-block.pub, a public key whose
# private half was never kept, so no update package can pass the signature check. The vendor key is
# kept as ota_script.pub.orig (restore: copy it back). rootfs write, remount rw/ro around it.
ota-block:
	$(SSH) 'cat > /tmp/ota-block.pub' < init.d/ota-block.pub
	printf '%s\n' 'set -e' 'Q=/system/vendor/quasar' 'mount -o remount,rw /' \
	  'trap "sync; mount -o remount,ro /" EXIT' \
	  '[ -f $$Q/ota_script.pub.orig ] || cp -p $$Q/ota_script.pub $$Q/ota_script.pub.orig' \
	  'cp /tmp/ota-block.pub $$Q/ota_script.pub' 'rm -f /tmp/ota-block.pub' \
	  'cmp -s $$Q/ota_script.pub $$Q/ota_script.pub.orig && { echo "ota-block: key unchanged"; exit 1; }' \
	  'echo "ota-block: vendor key replaced (.orig kept)"' | $(SSH) 'sh -s'

# Vendor sounds the daemon plays with aplay, which only does wav. The device copies are opus or
# mp3 (bergamot 0.369: ru-RU/no_power.opus; pickle and bergamot 0.6.x: an Ogg/Opus file *named*
# .wav; the volume tick: common/volume_sound.mp3, or sounds/volume_sound.mp3 on 0.6.x), and
# there's no decoder on board: pull, decode on the host, put a real wav under $(DESTDIR)/sounds/.
# Vendor audio, so it's never stored in the repo. A failed decode leaves the old file alone.
SOUNDS ?= no_power volume_sound notification_sound something_went_wrong backend_unavailable \
	calls_connected calls_ended calls_busy calls_ringing_incoming calls_ringing_outgoing
# Per-sound ffmpeg filters: the announce chime is the stock notification sound, made quieter; the
# error phrase is cut after «К сожалению, что-то пошло не так.» (its second sentence asks to redo
# the setup; the file is the same on every firmware we have, the pause is at 2.41-3.15 s).
AF_notification_sound := -af volume=-8dB
AF_something_went_wrong := -af atrim=0:2.6,afade=t=out:st=2.45:d=0.15
sounds:
	$(SSH) 'mkdir -p $(DESTDIR)/sounds'
	set -e; for s in $(SOUNDS); do \
	  if ! $(SSH) "d=/system/vendor/quasar/sounds; for f in ru-RU/$$s.opus ru-RU/$$s.wav ru-RU/$$s.mp3 common/$$s.mp3 $$s.mp3; do [ -f \$$d/\$$f ] && exec cat \$$d/\$$f; done; exit 1" > build/$$s.src; then \
	    echo "  $$s: not on this firmware, skipped"; rm -f build/$$s.src; continue; \
	  fi; \
	  case $$s in notification_sound) af="$(AF_notification_sound)";; something_went_wrong) af="$(AF_something_went_wrong)";; *) af=;; esac; \
	  ffmpeg -v error -y -i build/$$s.src $$af -ar 48000 -ac 2 build/$$s.wav; \
	  $(SSH) "cat > $(DESTDIR)/sounds/$$s.wav.new && mv $(DESTDIR)/sounds/$$s.wav.new $(DESTDIR)/sounds/$$s.wav" < build/$$s.wav; \
	  rm -f build/$$s.src build/$$s.wav; \
	done

# The VQE's call-mode noise reducer (docs/intercom.md, "The VQE call mode"). spotter-host loads it
# as <models>/noise_reducer/model.nnet; firmware 0.369 ships it as model_call.nnet, 3.21 ships
# none. Copied from the unit's own firmware when it has one, else from VQE_MODEL (a local file,
# e.g. from your own firmware dump). Vendor data, never stored in the repo.
VQE_MODEL ?=
vqe-models:
	$(SSH) 'mkdir -p $(DESTDIR)/vendor/vqe_models/noise_reducer'
	set -e; d=/system/vendor/quasar/vqe_models/noise_reducer; t=$(DESTDIR)/vendor/vqe_models/noise_reducer; \
	if $(SSH) "[ -f $$d/model_call.nnet ]"; then \
	  $(SSH) "cp $$d/model_call.nnet $$t/model.nnet.new && mv $$t/model.nnet.new $$t/model.nnet && \
	    { [ -f $$d/model_call_meta.json ] && cp $$d/model_call_meta.json $$t/model_meta.json || true; }"; \
	  echo "  from the unit's firmware"; \
	elif [ -n "$(VQE_MODEL)" ]; then \
	  $(SSH) "cat > $$t/model.nnet.new && mv $$t/model.nnet.new $$t/model.nnet" < "$(VQE_MODEL)"; \
	  m=$$(dirname "$(VQE_MODEL)")/model_call_meta.json; \
	  [ -f "$$m" ] && $(SSH) "cat > $$t/model_meta.json" < "$$m" || true; \
	  echo "  from $(VQE_MODEL)"; \
	else \
	  echo "  no call-mode model on this firmware: pass VQE_MODEL=<model_call.nnet>"; exit 1; \
	fi; \
	$(SSH) "ls -la $$t"

# The video display mode's files (docs/video-mode.md) from a local video you supply: VIDEO=<file>.
# video-convert only converts (cmd/videoconv, ffmpeg) into build/media/; video also uploads them to
# $(DESTDIR)/media/ (the daemon's default video_frames / video_audio). VIDEO_FLAGS go to videoconv
# (-fit crop, -fps 25). Third-party media: never stored in the repo.
VIDEO ?=
VIDEO_FLAGS ?=
video-convert:
	@test -n "$(VIDEO)" || { echo "usage: make video VIDEO=<local video file>"; exit 2; }
	go build -o build/videoconv ./cmd/videoconv
	build/videoconv $(VIDEO_FLAGS) -frames build/media/video.led.gz -audio build/media/video.wav "$(VIDEO)"
video: video-convert
	$(SSH) 'mkdir -p $(DESTDIR)/media'
	set -e; for f in video.led.gz video.wav; do \
	  $(SSH) "cat > $(DESTDIR)/media/$$f.new && mv $(DESTDIR)/media/$$f.new $(DESTDIR)/media/$$f" < build/media/$$f; \
	done

# Stock clock animation clips (docs/display-expressions.md). Firmware 0.369 ships them as
# zstd-compressed binary .binscled, and the device has no zstd: pull, decompress and convert to the
# text .scled on the host (cmd/clipconv), put them under $(DESTDIR)/clips/. Firmware that already
# ships text .scled needs nothing (the daemon reads the firmware's directory too). Vendor data, so
# never stored in the repo.
CLIPS_SRC ?= /system/vendor/quasar/ledpatterns/clock
clips:
	go build -o build/clipconv ./cmd/clipconv
	@mkdir -p build/clips
	set -e; names=$$($(SSH) 'cd $(CLIPS_SRC) 2>/dev/null && ls *.binscled.zst 2>/dev/null' | sed 's/\.binscled\.zst$$//'); \
	if [ -z "$$names" ]; then echo "no binary clips on $(DEVICE): nothing to convert"; exit 0; fi; \
	$(SSH) 'mkdir -p $(DESTDIR)/clips'; \
	for n in $$names; do \
	  $(SSH) "cat $(CLIPS_SRC)/$$n.binscled.zst" | zstd -q -d -c > build/clips/$$n.binscled; \
	  build/clipconv build/clips/$$n.binscled > build/clips/$$n.scled; \
	  $(SSH) "cat > $(DESTDIR)/clips/$$n.scled.new && mv $(DESTDIR)/clips/$$n.scled.new $(DESTDIR)/clips/$$n.scled" < build/clips/$$n.scled; \
	  echo "  $$n"; \
	done; rm -rf build/clips

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

# Install a sendspin-host build (../sendspin-host, `make ARCH=<arch>` there, matching the device's
# userland). Like spotter-host it takes effect when yastationd next starts it.
SENDSPIN ?= ../sendspin-host/build/$(HELPER_ARCH)/sendspin-host
deploy-sendspin:
	$(SSH) 'cat > $(DESTDIR)/bin/sendspin-host.upload && chmod +x $(DESTDIR)/bin/sendspin-host.upload && mv $(DESTDIR)/bin/sendspin-host.upload $(DESTDIR)/bin/sendspin-host' < $(SENDSPIN)

# Install a spotter-host build (spotter-host/build.sh, ARCH matching the device's userland). The
# running child keeps the old file; it takes effect when yastationd next starts it — restart the
# daemon (make deploy, or the init hook's restart).
SPOTTER ?= ../spotter-host/build/$(HELPER_ARCH)/spotter-host
deploy-spotter:
	$(SSH) 'cat > $(DESTDIR)/bin/spotter-host.upload && chmod +x $(DESTDIR)/bin/spotter-host.upload && mv $(DESTDIR)/bin/spotter-host.upload $(DESTDIR)/bin/spotter-host' < $(SPOTTER)
