#!/usr/bin/env bash
# The image is built out of a distribution handed to the build, as its README says.
set -eu
dir="$1"
version=6.1.4
dist="apache-activemq-$version-bin.tar.gz"

[ -f "$dir/$dist" ] && exit 0
curl -fL --retry 3 -o "$dir/$dist" \
    "https://archive.apache.org/dist/activemq/$version/$dist"
