#!/usr/bin/env bash
# Run the local eval task set and print pass rate, steps, tokens, and
# edit-failure rate. The model comes from the environment. Results are not
# committed; this script prints them.
#
#   SIGMA_BASE_URL  required unless --dry-run
#   SIGMA_MODEL     required unless --dry-run
#   SIGMA_API_KEY   optional
#   SIGMA_BIN       sigma binary (default: go run ./cmd/sigma)
#
#   eval/run.sh --dry-run     check the task layout and print the metric names
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

dry=0
if [[ "${1:-}" == "--dry-run" ]]; then
  dry=1
elif [[ $# -gt 0 ]]; then
  echo "usage: eval/run.sh [--dry-run]" >&2
  exit 2
fi

shopt -s nullglob
tasks=(eval/tasks/*)
if [[ ${#tasks[@]} -eq 0 ]]; then
  echo "no tasks in eval/tasks" >&2
  exit 1
fi

count=0
for dir in "${tasks[@]}"; do
  [[ -d "$dir" ]] || continue
  name=$(basename "$dir")
  if [[ ! -f "$dir/prompt.txt" || ! -f "$dir/check.sh" ]]; then
    echo "task $name needs prompt.txt and check.sh" >&2
    exit 1
  fi
  count=$((count + 1))
done

if [[ "$dry" == 1 ]]; then
  echo "dry-run: ${count} tasks"
  echo "metrics: pass_rate steps tokens edit_failure_rate"
  exit 0
fi

if [[ -z "${SIGMA_BASE_URL:-}" || -z "${SIGMA_MODEL:-}" ]]; then
  echo "set SIGMA_BASE_URL and SIGMA_MODEL (SIGMA_API_KEY if the server needs one)" >&2
  exit 1
fi

sigma=()
if [[ -n "${SIGMA_BIN:-}" ]]; then
  sigma=("$SIGMA_BIN")
else
  sigma=(go run ./cmd/sigma)
fi

pass=0
steps=0
tokens=0
edits=0
edit_fail=0
mkdir -p eval/results

for dir in "${tasks[@]}"; do
  [[ -d "$dir" ]] || continue
  name=$(basename "$dir")
  work=$(mktemp -d)
  if [[ -d "$dir/repo" ]]; then
    cp -a "$dir/repo/." "$work/"
  fi
  prompt=$(cat "$dir/prompt.txt")
  set +e
  json=$("${sigma[@]}" --mode auto --output json --workspace "$work" -p "$prompt" 2>"$work/sigma.err")
  code=$?
  set -e
  printf '%s\n' "$json" > "eval/results/${name}.json"
  if [[ -s "$work/sigma.err" ]]; then
    cp "$work/sigma.err" "eval/results/${name}.err"
  fi
  ok=0
  if [[ "$code" -eq 0 ]]; then
    if WORKSPACE="$work" bash "$dir/check.sh"; then
      ok=1
      pass=$((pass + 1))
    fi
  fi
  read -r tsteps ttok tedits tfail < <(python3 - "$json" << 'PY'
import json, sys
raw = sys.argv[1]
try:
    doc = json.loads(raw) if raw.strip() else {}
except json.JSONDecodeError:
    doc = {}
steps = int(doc.get("steps") or 0)
tokens = int(doc.get("prompt_tokens") or 0) + int(doc.get("completion_tokens") or 0)
edits = int(doc.get("edit_attempts") or 0)
fail = int(doc.get("edit_failures") or 0)
print(steps, tokens, edits, fail)
PY
)
  steps=$((steps + tsteps))
  tokens=$((tokens + ttok))
  edits=$((edits + tedits))
  edit_fail=$((edit_fail + tfail))
  echo "$name pass=$ok steps=$tsteps tokens=$ttok edit_failures=$tfail"
  rm -rf "$work"
done

python3 - "$count" "$pass" "$steps" "$tokens" "$edits" "$edit_fail" << 'PY'
import sys
n, passed, steps, tokens, edits, fails = map(int, sys.argv[1:])
rate = passed / n if n else 0
edit_rate = fails / edits if edits else 0
print(f"pass_rate={rate:.3f} ({passed}/{n})")
print(f"steps={steps}")
print(f"tokens={tokens}")
print(f"edit_failure_rate={edit_rate:.3f} ({fails}/{edits})")
PY
