#!/usr/bin/env bash
# The repository is the one the openliberty scenario fetches, into its own directory.
exec "$1/../openliberty/prepare.sh" "$1/../openliberty"
