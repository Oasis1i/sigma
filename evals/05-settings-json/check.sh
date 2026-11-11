#!/bin/sh
set -eu
grep -Eq '"name"[[:space:]]*:[[:space:]]*"sigma"' settings.json
grep -Eq '"debug"[[:space:]]*:[[:space:]]*false' settings.json
