#!/usr/bin/env bash
#
# Builds one of the Dockerfiles beside this script into a package and puts the package to work.
# A scenario is a directory holding a Dockerfile, whatever it needs, and an 'expect' file saying
# what should come of it:
#
#   DOCKERFILE       the file to build, when it is not named Dockerfile
#   EXPECT           ok, package when the package is as far as the scenario goes, or refuse when
#                    the Dockerfile describes something a unikernel cannot run
#   ERROR_MATCH      what the refusal has to say
#   OUTPUT_MATCH     what the program has to print, or answer over its port
#   PORT             the port to ask, for a program that serves rather than prints
#   URL_PATH         what to ask for on that port, / by default
#   BOOT_WAIT        how many two second turns to wait for the unikernel, 240 by default
#   BUILD_FLAGS      extra flags for 'ops pkg from-dockerfile'
#   MANIFEST_CHECK   a jq filter over the manifest that has to hold

set -u

scenario="${1:-}"
if [ -z "$scenario" ]; then
    echo "usage: $0 <scenario>" >&2
    exit 2
fi

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
dir="$here/$scenario"
ops="${OPS:-ops}"

if [ ! -d "$dir" ]; then
    echo "no such scenario: $scenario" >&2
    exit 2
fi

DOCKERFILE=Dockerfile
EXPECT=ok
ERROR_MATCH=""
OUTPUT_MATCH=""
PORT=""
URL_PATH="/"
BOOT_WAIT=240
BUILD_FLAGS=""
MANIFEST_CHECK=""
# shellcheck disable=SC1091
. "$dir/expect"

# Some of these images are built out of an artifact that has to be made first, the way their own
# instructions say to make it.
if [ -x "$dir/prepare.sh" ]; then
    echo "=== $scenario: preparing what the build needs"
    if ! "$dir/prepare.sh" "$dir"; then
        echo "FAIL $scenario: could not prepare the build context"
        exit 1
    fi
fi

arch="$(uname -m)"
case "$arch" in
    x86_64) arch=amd64 ;;
    aarch64) arch=arm64 ;;
esac

pkg="dockerfile-test-$scenario"
log="$(mktemp)"
boot="$(mktemp)"
trap 'rm -f "$log" "$boot"' EXIT

fail() {
    echo "FAIL $scenario: $1"
    shift
    [ $# -gt 0 ] && printf '%s\n' "$@"
    if [ -s "$boot" ]; then
        echo "--- last of what the unikernel said ---"
        tr '\r' '\n' < "$boot" | grep -vE "^ *[0-9]+% \||^ *$" | tail -200
    else
        echo "--- last of the log ---"
        tail -30 "$log"
    fi
    exit 1
}

echo "=== $scenario: from-dockerfile"
# shellcheck disable=SC2086
"$ops" pkg from-dockerfile "$dir/$DOCKERFILE" --name "$pkg" $BUILD_FLAGS > "$log" 2>&1
built=$?

if [ "$EXPECT" = refuse ]; then
    if [ $built -eq 0 ]; then
        fail "a package was built out of something a unikernel cannot run"
    fi
    if ! grep -q -- "$ERROR_MATCH" "$log"; then
        fail "refused, but not for the expected reason (wanted: $ERROR_MATCH)"
    fi
    echo "PASS $scenario: refused, and said why"
    grep -m1 -- "$ERROR_MATCH" "$log"
    exit 0
fi

[ $built -eq 0 ] || fail "the package could not be built"

# how much of the image the package carries, and what was left out of its operating system
grep -E '^(the file system is|left out|kept|warning: |the image starts|reading |the container)' "$log"

manifest="$HOME/.ops/local_packages/$arch/$pkg/package.manifest"
[ -f "$manifest" ] || fail "no manifest at $manifest"
echo "--- manifest ---"
cat "$manifest"

if [ -n "$MANIFEST_CHECK" ]; then
    if [ "$(jq "$MANIFEST_CHECK" < "$manifest")" != "true" ]; then
        fail "the manifest does not hold: $MANIFEST_CHECK"
    fi
    echo "manifest: $MANIFEST_CHECK holds"
fi

if [ "$EXPECT" = package ]; then
    echo "PASS $scenario: the package is what it should be"
    exit 0
fi

echo "=== $scenario: booting the package"
if [ -n "$PORT" ]; then
    timeout 1800 "$ops" pkg load -l "$pkg" --accel=false -p "$PORT" > "$boot" 2>&1 &
    runner=$!

    # There is no kvm on a hosted runner, so a jvm starting under emulation is slow enough to
    # need the wait to be measured in minutes rather than seconds.
    # ops returns once the machine is up, while the machine goes on running, so the thing to
    # wait on is the answer rather than the process.
    answer=""
    for _ in $(seq 1 "$BOOT_WAIT"); do
        answer="$(curl -sS --max-time 2 "http://127.0.0.1:$PORT$URL_PATH" 2>/dev/null)"
        [ -n "$answer" ] && break
        sleep 2
    done

    kill $runner 2>/dev/null
    pkill -f qemu-system 2>/dev/null
    wait $runner 2>/dev/null

    printf '%s' "$answer" | grep -q -- "$OUTPUT_MATCH" || \
        fail "the server did not answer with what it should (wanted: $OUTPUT_MATCH)" "got: $answer"
else
    timeout 1800 "$ops" pkg load -l "$pkg" --accel=false > "$boot" 2>&1 &
    runner=$!

    # Whether the program prints and stops or prints and stays, what is being waited for is the
    # line, not the end of it.
    said=no
    for _ in $(seq 1 "$BOOT_WAIT"); do
        sleep 2

        # read after the wait, so that a program which prints and stops is still read
        if grep -q -- "$OUTPUT_MATCH" "$boot"; then
            said=yes
            break
        fi
    done

    kill $runner 2>/dev/null
    pkill -f qemu-system 2>/dev/null
    wait $runner 2>/dev/null

    [ "$said" = yes ] || \
        fail "the program did not print what it should (wanted: $OUTPUT_MATCH)"
fi

echo "PASS $scenario"
