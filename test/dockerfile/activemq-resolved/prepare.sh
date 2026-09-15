#!/usr/bin/env bash
# The distribution is the one the activemq scenario fetches, into its own directory.
exec "$1/../activemq/prepare.sh" "$1/../activemq"
