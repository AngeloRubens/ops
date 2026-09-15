#!/usr/bin/env bash
# The repository is the one the tomee scenario fetches, into its own directory.
exec "$1/../tomee/prepare.sh" "$1/../tomee"
