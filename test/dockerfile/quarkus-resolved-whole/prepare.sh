#!/usr/bin/env bash
# The application is the one the quarkus scenario builds, in its own directory.
exec "$1/../quarkus/prepare.sh" "$1/../quarkus"
