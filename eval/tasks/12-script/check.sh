#!/bin/sh
set -eu
cd "$WORKSPACE"
chmod +x script.sh
out=$(./script.sh)
test "$out" = "ok"
