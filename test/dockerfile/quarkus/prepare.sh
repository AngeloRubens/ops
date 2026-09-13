#!/usr/bin/env bash
# The Dockerfile expects target/quarkus-app, which is what packaging a quarkus project makes.
set -eu
dir="$1"
[ -d "$dir/target/quarkus-app" ] && exit 0

docker run --rm -v "$dir":/ws -w /ws maven:3.9-eclipse-temurin-21 sh -c '
    set -eu
    mvn -B -q io.quarkus.platform:quarkus-maven-plugin:3.15.1:create \
        -DprojectGroupId=ops -DprojectArtifactId=app -Dextensions=rest
    cd app && mvn -B -q package -DskipTests
'
mkdir -p "$dir/target"
cp -r "$dir/app/target/quarkus-app" "$dir/target/quarkus-app"
