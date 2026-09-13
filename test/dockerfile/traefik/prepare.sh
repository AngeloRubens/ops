#!/usr/bin/env bash
# The image is built from the repository traefik keeps its official image in.
set -eu
dir="$1"
[ -d "$dir/upstream" ] && exit 0
git clone --depth 1 https://github.com/traefik/traefik-library-image.git "$dir/upstream"
