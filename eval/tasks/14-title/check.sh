#!/bin/sh
set -eu
cd "$WORKSPACE"
test "$(cat titles.txt)" = "Hello, world"
