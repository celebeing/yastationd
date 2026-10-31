# tools/golden.sh: building a golden restore image. UBI/UBIFS for NAND boards, ext4 and Android
# sparse images for eMMC ones, fakeroot to keep the rootfs's owners when running as the calling
# user, ubi_reader to unpack the result and compare it. The Debian packages are bookworm's.
FROM debian:bookworm-20240722-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
	mtd-utils e2fsprogs android-sdk-libsparse-utils fakeroot python3 python3-pip && \
	pip3 install --break-system-packages --no-cache-dir ubi_reader==0.8.10 && \
	rm -rf /var/lib/apt/lists/*
