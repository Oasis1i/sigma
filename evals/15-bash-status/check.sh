#!/bin/sh
set -eu
printf 'ready\n' | cmp -s - status.txt
