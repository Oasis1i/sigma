#!/bin/sh
set -eu
out=$(sh script.sh)
test "$out" = "hello"
