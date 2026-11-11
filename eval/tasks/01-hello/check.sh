#!/bin/sh
set -eu
cd "$WORKSPACE"
test "$(cat hello.txt)" = "hello"
