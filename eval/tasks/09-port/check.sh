#!/bin/sh
set -eu
cd "$WORKSPACE"
test "$(cat config.txt)" = "host=127.0.0.1
port=9090"
