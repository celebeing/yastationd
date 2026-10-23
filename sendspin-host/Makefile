# sendspin-host — cross-build with zig, one binary per userland ABI:
#   ARCH=armhf    bergamot (32-bit ARM hard-float userland on an arm64 kernel, glibc 2.30)
#   ARCH=aarch64  pickle   (aarch64, glibc 2.36)
# `make` builds both; `make ARCH=armhf` one. Output: build/<arch>/sendspin-host (stripped) and
# build/<arch>/sendspin-host.debug.
#
# sendspin-cpp and its dependencies are fetched once (pinned, third_party/fetch.sh) into
# third_party/src/ and built per ARCH with CMake + Ninja (third_party/CMakeLists.txt). libasound is
# linked against a sysroot built from the alsa-lib release tarball (third_party/sysroot.sh); at
# runtime the firmware's own libasound is used. C++ runtime: zig's libc++, linked statically, so
# the binary needs only glibc, libasound and libdl/libpthread from the device.
# Host tools: zig 0.14.1 (0.13 produces armhf C++ binaries that crash before main, per pulsar),
# git, curl, cmake >= 3.16, ninja, make. ZIG=/path/to/zig for a zig outside PATH.
# The build layout follows pulsar's satellite/Makefile (MIT, (c) 2026 pulsar contributors).
ZIG  ?= zig
ARCHES := armhf aarch64

ifeq ($(ARCH),)

all: $(ARCHES)
$(ARCHES):
	+$(MAKE) --no-print-directory ARCH=$@ binary

clean:
	rm -rf build

distclean: clean
	rm -rf third_party/src

.PHONY: all clean distclean $(ARCHES)

else

ifeq ($(ARCH),aarch64)
TARGET := aarch64-linux-gnu.2.36
CPU    :=
HOST   := aarch64-linux-gnu
ALSA   :=
else ifeq ($(ARCH),armhf)
TARGET := arm-linux-gnueabihf.2.30
CPU    := -mcpu=cortex_a53
HOST   := arm-linux-gnueabihf
ALSA   := --with-versioned=no
else
$(error ARCH must be one of: $(ARCHES))
endif

ZIG_VERSION := $(shell $(ZIG) version 2>/dev/null)
ifneq ($(filter 0.11.% 0.12.% 0.13.%,$(ZIG_VERSION)),)
$(error zig $(ZIG_VERSION) is too old (armhf C++ binaries crash before main); use zig 0.14.1)
endif

export ZIG
CXX      := $(ZIG) c++ -target $(TARGET) $(CPU)
BUILD    := build/$(ARCH)
SYSROOT  := $(BUILD)/sysroot
SYSROOT_STAMP := $(SYSROOT)/.built

SS_SRC   := third_party/src
SS_BUILD := $(BUILD)/third_party
SS_STAMP := $(SS_BUILD)/.built
SS_DEFS  := -DSENDSPIN_ENABLE_PLAYER
SS_LIBS  := $(SS_BUILD)/sendspin/libsendspin.a \
            $(SS_BUILD)/_deps/micro_flac-build/libmicro_flac.a \
            $(SS_BUILD)/_deps/ixwebsocket-build/libixwebsocket.a \
            $(SS_BUILD)/sendspin/libnoise_c.a

VERSION  := $(shell git describe --always --dirty 2>/dev/null || echo unknown)
CXXFLAGS := -std=c++20 -O2 -Wall -Wextra -Wno-unused-parameter -Isrc -I$(SYSROOT)/include \
            -isystem $(SS_SRC)/sendspin-cpp/include -isystem $(SS_SRC)/sendspin-cpp/examples/common \
            -isystem $(SS_SRC)/ArduinoJson/src $(SS_DEFS) -DSENDSPIN_HOST_VERSION='"$(VERSION)"' -g1
LDFLAGS  := -L$(SYSROOT)/lib -Wl,--start-group $(SS_LIBS) -Wl,--end-group -lasound -ldl -lpthread

# Our sources plus the library's example file-backed persistence provider (Apache-2.0, built from
# the fetched tree, not copied into this repo).
SRCS := $(wildcard src/*.cpp)
OBJS := $(patsubst src/%.cpp,$(BUILD)/%.o,$(SRCS)) $(BUILD)/file_persistence_provider.o
DEPS := $(OBJS:.o=.d)

binary: $(BUILD)/sendspin-host

$(BUILD)/sendspin-host.debug: $(OBJS) $(SS_STAMP)
	$(CXX) $(OBJS) -o $@ $(LDFLAGS)

$(BUILD)/sendspin-host: $(BUILD)/sendspin-host.debug
	$(ZIG) objcopy --strip-all $< $@
	@ls -l $@

$(BUILD)/%.o: src/%.cpp | $(SS_STAMP) $(SYSROOT_STAMP)
	@mkdir -p $(dir $@)
	$(CXX) $(CXXFLAGS) -MMD -MP -c $< -o $@

$(BUILD)/file_persistence_provider.o: | $(SS_STAMP)
	@mkdir -p $(dir $@)
	$(CXX) $(CXXFLAGS) -MMD -MP -c $(SS_SRC)/sendspin-cpp/examples/common/file_persistence_provider.cpp -o $@

$(SYSROOT_STAMP): third_party/sysroot.sh
	env -u MAKEFLAGS -u MFLAGS third_party/sysroot.sh $(SYSROOT) "$(TARGET) $(CPU)" $(HOST) "$(ALSA)"
	touch $@

# Configured from inside the build dir: CMake's compiler probe makes zig write a file named "-".
$(SS_STAMP): third_party/fetch.sh third_party/CMakeLists.txt third_party/toolchain/zig.cmake
	third_party/fetch.sh
	@mkdir -p $(SS_BUILD)
	cd $(SS_BUILD) && ZIG_TARGET=$(TARGET) ZIG_CPU="$(CPU)" cmake -S $(CURDIR)/third_party -B . -G Ninja \
	  -DCMAKE_BUILD_TYPE=Release -DCMAKE_TOOLCHAIN_FILE=$(CURDIR)/third_party/toolchain/zig.cmake \
	  -DCMAKE_POLICY_VERSION_MINIMUM=3.5 >/dev/null
	env -u MAKEFLAGS -u MFLAGS ZIG_TARGET=$(TARGET) ZIG_CPU="$(CPU)" ninja -C $(SS_BUILD)
	touch $@

.PHONY: binary
-include $(DEPS)

endif
