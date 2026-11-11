#!/usr/bin/env bash
# Run Sigma eval tasks one-shot in auto mode.
# Each task needs a live model, so this script is not part of CI.
#
# Configure Sigma first (~/.config/sigma/config.toml, .sigma.toml, or SIGMA_*).
# Override the binary with SIGMA=/path/to/sigma and the step cap with
# SIGMA_EVAL_MAX_STEPS (default 30).

set -uo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SIGMA=${SIGMA:-"$ROOT/sigma"}
MAX_STEPS=${SIGMA_EVAL_MAX_STEPS:-30}

if [[ ! -x "$SIGMA" ]]; then
  echo "building $SIGMA"
  (cd "$ROOT" && go build -o "$SIGMA" ./cmd/sigma)
fi

shopt -s nullglob
tasks=("$ROOT"/evals/*/)
if ((${#tasks[@]} == 0)); then
  echo "no eval tasks under $ROOT/evals" >&2
  exit 1
fi

pass=0
fail=0
failed=()

for dir in "${tasks[@]}"; do
  name=$(basename "$dir")
  tmp=$(mktemp -d)
  if [[ -d "$dir/workspace" ]]; then
    cp -a "$dir/workspace/." "$tmp/"
  fi
  prompt=$(<"$dir/prompt.txt")
  echo "=== $name ==="
  if "$SIGMA" --workspace "$tmp" --mode auto --max-steps "$MAX_STEPS" -p "$prompt"; then
    if (cd "$tmp" && bash "$dir/check.sh"); then
      echo "PASS $name"
      pass=$((pass + 1))
    else
      echo "FAIL $name (check)"
      fail=$((fail + 1))
      failed+=("$name")
    fi
  else
    echo "FAIL $name (agent)"
    fail=$((fail + 1))
    failed+=("$name")
  fi
  rm -rf "$tmp"
done

echo
echo "passed $pass  failed $fail"
if ((fail > 0)); then
  printf 'failed:'
  printf ' %s' "${failed[@]}"
  printf '\n'
  exit 1
fi
