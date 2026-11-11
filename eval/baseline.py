#!/usr/bin/env python3
"""Optional mini-swe-agent baseline for the same SWE-bench ids and model.

This does not vendor mini-swe-agent. Install it yourself if you want the
baseline. --dry-run only prints the command it would run and does not import
the package or call a model.

  SIGMA_BASE_URL, SIGMA_MODEL, SIGMA_API_KEY
  python3 eval/baseline.py --dry-run --limit 2
"""

from __future__ import annotations

import argparse
import os
import shutil
import sys


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--instances", default="eval/swebench_lite_ids.txt")
    parser.add_argument("--limit", type=int, default=20)
    parser.add_argument("--output", default="eval/results/baseline.jsonl")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    if args.limit < 1 or args.limit > 50:
        raise SystemExit("--limit must be from 1 to 50")

    ids = [
        ln.strip()
        for ln in open(args.instances, encoding="utf-8")
        if ln.strip() and not ln.startswith("#")
    ][: args.limit]
    model = os.environ.get("SIGMA_MODEL", "")
    base = os.environ.get("SIGMA_BASE_URL", "")
    exe = shutil.which("mini") or "mini"
    print("baseline command:")
    print(
        f"  {exe} --model {model or '$SIGMA_MODEL'} "
        f"--base-url {base or '$SIGMA_BASE_URL'} "
        f"--instances {','.join(ids[:2])}{'...' if len(ids) > 2 else ''} "
        f"--output {args.output}"
    )
    print("install: pip install mini-swe-agent")
    if args.dry_run:
        print(f"dry-run: {len(ids)} instances, model not called")
        return
    if not model or not base:
        raise SystemExit("set SIGMA_BASE_URL and SIGMA_MODEL")
    if shutil.which("mini") is None:
        raise SystemExit("mini-swe-agent is not installed (no `mini` on PATH)")
    print("run the command above; this driver does not wrap the agent's Python API", file=sys.stderr)
    raise SystemExit(2)


if __name__ == "__main__":
    main()
