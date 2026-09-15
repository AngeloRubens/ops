#!/usr/bin/env bash
# The image is the one the rocketmq scenario builds out of its own repository, built here first so
# that the Dockerfile beside this can name the role to start: the image's CMD is a placeholder.
set -eu
dir="$1"
"$dir/../rocketmq/prepare.sh" "$dir/../rocketmq"
docker image inspect ops-test-rocketmq:5.3.2 >/dev/null 2>&1 && exit 0
docker build --build-arg version=5.3.2 -t ops-test-rocketmq:5.3.2 \
    -f "$dir/../rocketmq/upstream/image-build/Dockerfile-ubuntu" "$dir/../rocketmq/upstream/image-build"
