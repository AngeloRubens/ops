#!/usr/bin/env bash
# The dockerfile prometheus publishes copies the binaries its own build makes, so this lays out
# the release the way that build leaves it.
set -eu
dir="$1"
version=3.6.0
[ -f "$dir/.build/linux-amd64/prometheus" ] && exit 0

curl -fL --retry 3 -o "$dir/prometheus.tar.gz" \
    "https://github.com/prometheus/prometheus/releases/download/v$version/prometheus-$version.linux-amd64.tar.gz"
tar -xzf "$dir/prometheus.tar.gz" -C "$dir"
release="$dir/prometheus-$version.linux-amd64"

mkdir -p "$dir/.build/linux-amd64" "$dir/documentation/examples"
cp "$release/prometheus" "$release/promtool" "$dir/.build/linux-amd64/"
cp "$release/prometheus.yml" "$dir/documentation/examples/"
cp "$release/LICENSE" "$release/NOTICE" "$dir/"
rm -rf "$release" "$dir/prometheus.tar.gz"

curl -fL --retry 3 -o "$dir/Dockerfile" \
    https://raw.githubusercontent.com/prometheus/prometheus/main/Dockerfile
