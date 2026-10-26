# yastationd: Go, CGO off, one static armv7 binary for every board (tools/build.sh yastationd).
FROM golang:1.22.5-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends file && rm -rf /var/lib/apt/lists/*
ENV GOTOOLCHAIN=local
