#!/usr/bin/env bash
# The repository is the one the temurin scenario fetches, into its own directory.
exec "$1/../temurin/prepare.sh" "$1/../temurin"
