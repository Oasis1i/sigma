#!/bin/sh
set -eu
go test ./...
if grep -n TODO *.go; then
  echo "TODO comment remains" >&2
  exit 1
fi
