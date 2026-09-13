#!/usr/bin/env bash
# The dockerfile coredns publishes copies the binary its own build makes.
set -eu
dir="$1"
version=1.12.4
[ -f "$dir/coredns" ] && exit 0

curl -fL --retry 3 -o "$dir/coredns.tgz" \
    "https://github.com/coredns/coredns/releases/download/v$version/coredns_${version}_linux_amd64.tgz"
tar -xzf "$dir/coredns.tgz" -C "$dir" coredns
rm -f "$dir/coredns.tgz"

curl -fL --retry 3 -o "$dir/Dockerfile" \
    https://raw.githubusercontent.com/coredns/coredns/master/Dockerfile
