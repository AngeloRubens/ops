#!/usr/bin/env bash
# The dockerfile expects a built jar in target/, which is what packaging the project makes.
set -eu
dir="$1"
[ -n "$(ls "$dir"/target/*.jar 2>/dev/null)" ] && exit 0

docker run --rm -v "$dir":/ws -w /ws maven:3.9-eclipse-temurin-21 \
    mvn -B -q package -DskipTests
