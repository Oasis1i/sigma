#!/bin/sh
set -eu
printf 'host=localhost\nport=9090\n' | cmp -s - config.txt
