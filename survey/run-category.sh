#!/usr/bin/env bash
#
# Takes the most pulled images of one docker hub category and asks what becomes of each: a
# package, a package once the image is asked what it starts, or a refusal and which one - and
# then whether the machine built out of it runs.
#
#   ./survey/run-category.sh databases-and-storage [how many]
#
# An image that will not start without being told something - postgres wants a password, bitnami
# wants to be told an empty one is meant - is given what its own documentation says to give it,
# from survey/config. Otherwise the answer would be about the missing variable rather than about
# the image.
#
# A machine counts as running when it answers on the first port its image exposes, or when it
# says something of its own after the kernel has finished saying its piece. Nothing is asked of
# it beyond that: this is about how far the images of the world get, not what they do afterwards.

set -u

category="${1:-}"
many="${2:-3}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ops="${OPS:-ops}"
arch="$(uname -m)"; [ "$arch" = x86_64 ] && arch=amd64; [ "$arch" = aarch64 ] && arch=arm64

images="$(grep "^$category|" "$here/images.txt" | cut -d'|' -f2 | tr ',' ' ')"
if [ -z "$images" ]; then
    echo "no such category: $category" >&2
    exit 2
fi

# a runner has some fourteen gigabytes, and the package is the image over again
limit=$((3 * 1024 * 1024 * 1024))

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
log="$work/log"
boot="$work/boot"

# kept, because a machine that stays silent has usually said why somewhere
logs="${LOGS:-$PWD/survey-logs}"
mkdir -p "$logs"

taken=0

for image in $images; do
    [ "$taken" -ge "$many" ] && break
    taken=$((taken + 1))

    name="survey-$(echo "$image" | tr '/:' '--')"
    printf 'FROM %s\n' "$image" > "$work/Dockerfile"

    # what the image asks for in its own documentation before it will start: a password it will
    # not do without, a service to load, a command to serve rather than print its help
    configured="-"
    config="$here/config/$(echo "$image" | tr '/:' '--')"
    if [ -f "$config" ]; then
        cat "$config" >> "$work/Dockerfile"
        configured="configured"
    fi

    if ! timeout 600 docker pull -q "$image" > "$log" 2>&1; then
        printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$category" "$image" "unreachable" "-" "-" "$(tail -1 "$log" | cut -c1-110)"
        continue
    fi

    size="$(docker image inspect -f '{{.Size}}' "$image" 2>/dev/null || echo 0)"
    if [ "$size" -gt "$limit" ]; then
        printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$category" "$image" "too-large" "$((size / 1024 / 1024))MB" "-" "-"
        docker rmi -f "$image" > /dev/null 2>&1
        continue
    fi

    # what ops makes of it: a package, or a package once the image is asked what it starts
    outcome=""
    if timeout 900 "$ops" pkg from-dockerfile "$work/Dockerfile" --name "$name" --quiet > "$log" 2>&1; then
        outcome="package"
    elif grep -qE "runs a script|through a shell" "$log"; then
        if timeout 900 "$ops" pkg from-dockerfile "$work/Dockerfile" --name "$name" --quiet \
               --resolve-entrypoint --resolve-timeout 120 > "$log" 2>&1; then
            outcome="package-resolved"
        else
            outcome="unresolved"
        fi
    elif grep -q "neither an ENTRYPOINT nor a CMD" "$log"; then
        outcome="nothing-to-run"
    else
        outcome="build-failed"
    fi

    manifest="$HOME/.ops/local_packages/$arch/$name/package.manifest"
    program="-"
    ran="-"

    if [ -f "$manifest" ]; then
        program="$(jq -r '.Program // "-"' "$manifest")"
        port="$(jq -r '.RunConfig.Ports[0] // ""' "$manifest")"

        forward=""
        [ -n "$port" ] && [ "$port" -gt 1024 ] 2>/dev/null && forward="-p $port"

        # shellcheck disable=SC2086
        timeout 900 "$ops" pkg load -l "$name" --accel=false $forward > "$boot" 2>&1 &
        runner=$!

        ran="silent"
        for _ in $(seq 1 150); do
            sleep 2
            if [ -n "$forward" ] && [ -n "$(curl -sS --max-time 2 "http://127.0.0.1:$port/" 2>/dev/null)" ]; then
                ran="answers"
                break
            fi
            # anything the program says of its own, which is whatever comes after the line the
            # kernel prints as it starts and is not ops talking about the image it just made
            said="$(tr '\r' '\n' < "$boot" \
                | awk '/booting /{seen = 1; next} seen' \
                | grep -cvE "^ *[0-9]+% \||^ *$|assigned|^warning:|overwriting|^Bootable|created\.\.\.$")"
            if [ "${said:-0}" -gt 0 ]; then
                ran="speaks"
                break
            fi
        done

        kill $runner 2>/dev/null
        pkill -f qemu-system 2>/dev/null
        wait $runner 2>/dev/null
    fi

    # what the image said for itself, which is where the reason lives when there is one
    note="$(tr '\r' '\n' < "$boot" 2>/dev/null \
        | awk '/booting /{seen = 1; next} seen' \
        | grep -vE "^ *[0-9]+% \||^ *$|assigned|^warning:|overwriting|^Bootable|created\.\.\.$" \
        | head -1 | cut -c1-110)"
    [ -z "$note" ] && note="$(grep -m1 -aE "runs a script|through a shell|rewrites its own|nothing but the launcher|not in the image|Error|error" "$log" 2>/dev/null | cut -c1-110)"
    [ -z "$note" ] && note="-"

    cp "$log" "$logs/$name.build.log" 2>/dev/null
    [ -s "$boot" ] && cp "$boot" "$logs/$name.boot.log" 2>/dev/null

    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$category" "$image" "$outcome" "$program" "$ran" "$configured" "$note"

    rm -rf "$HOME/.ops/local_packages/$arch/$name" "$HOME/.ops/images/$(basename "$program")"
    docker rmi -f "$image" > /dev/null 2>&1
    docker image prune -f > /dev/null 2>&1
done
