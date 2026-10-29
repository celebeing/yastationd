# The host-side conversions (tools/build.sh make:<target>): decoding the firmware's sounds (ffmpeg),
# its zstd-compressed clock clips (zstd + cmd/clipconv), videos for the video mode (cmd/videoconv +
# ffmpeg), so the host needs only docker. The same Go as docker/go.Dockerfile; the Debian packages
# are bookworm's.
FROM golang:1.22.5-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends file ffmpeg zstd jq && rm -rf /var/lib/apt/lists/*
ENV GOTOOLCHAIN=local
