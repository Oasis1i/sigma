#!/bin/sh
set -eu
printf 'hello sigma\n' | cmp -s - hello.txt
