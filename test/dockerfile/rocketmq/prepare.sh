#!/usr/bin/env bash
# The image is built out of its own repository, so this fetches the repository and the build is
# run against it unchanged.
set -eu
dir="$1"
[ -d "$dir/upstream" ] && exit 0
git clone --depth 1 https://github.com/apache/rocketmq-docker.git "$dir/upstream"
