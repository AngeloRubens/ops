#!/usr/bin/env bash
# The dockerfile is the one the helidon archetype generates for every project, so this generates
# a project the way the helidon quickstart says to.
set -eu
dir="$1"
[ -f "$dir/app/Dockerfile" ] && exit 0

docker run --rm -v "$dir":/ws -w /ws maven:3.9-eclipse-temurin-21 sh -c '
    set -eu
    mvn -B -q -U archetype:generate -DinteractiveMode=false \
        -DarchetypeGroupId=io.helidon.archetypes \
        -DarchetypeArtifactId=helidon-quickstart-se \
        -DarchetypeVersion=4.1.4 \
        -DgroupId=ops -DartifactId=app -Dpackage=ops.app
    cd app && mvn -B -q package -DskipTests
'
