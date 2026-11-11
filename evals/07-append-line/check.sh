#!/bin/sh
set -eu
printf 'alpha\nbeta\ngamma\n' | cmp -s - notes.txt
