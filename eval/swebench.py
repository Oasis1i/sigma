#!/usr/bin/env python3
"""Write SWE-bench predictions.jsonl for a Lite subset.

The official Docker harness consumes this file. This script does not run
that harness. It asks Sigma for a patch per instance when a dataset file is
provided. Without a model key, use --dry-run: it writes the JSONL schema for
the first --limit ids and does not call a model.

Environment (required unless --dry-run):
  SIGMA_BASE_URL, SIGMA_MODEL
  SIGMA_API_KEY   optional
  SIGMA_BIN       optional path to sigma

  python3 eval/swebench.py --dry-run --limit 2 --output /tmp/predictions.jsonl
  python3 eval/swebench.py --instances eval/swebench_lite_ids.txt --limit 20 \\
      --dataset instances.jsonl --output eval/results/predictions.jsonl

--dataset is a JSONL file of SWE-bench instances with instance_id,
problem_statement, and a local repo checkout is not performed here. The
script runs sigma -p in --workspace if the instance has a "workdir" field;
otherwise it asks sigma to emit a unified diff as the whole reply and stores
that reply as model_patch. Preparing workdirs is the SWE-bench harness's job.
This driver only produces predictions.jsonl.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path


def ids_from(path: Path, limit: int) -> list[str]:
    lines = [ln.strip() for ln in path.read_text().splitlines()]
    out = [ln for ln in lines if ln and not ln.startswith("#")]
    if limit > 0:
        out = out[:limit]
    return out


def dry_predictions(ids: list[str], model: str) -> list[dict]:
    return [
        {
            "instance_id": instance_id,
            "model_name_or_path": model or "unset",
            "model_patch": "",
        }
        for instance_id in ids
    ]


def load_dataset(path: Path) -> dict[str, dict]:
    found = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        row = json.loads(line)
        found[row["instance_id"]] = row
    return found


def run_sigma(instance: dict, model: str) -> str:
    sigma = os.environ.get("SIGMA_BIN")
    cmd = [sigma] if sigma else ["go", "run", "./cmd/sigma"]
    prompt = (
        "Produce a unified git diff that fixes the issue. "
        "Reply with only the diff.\n\n"
        + instance.get("problem_statement", "")
    )
    workspace = instance.get("workdir") or os.getcwd()
    full = cmd + ["--mode", "auto", "--output", "json", "--workspace", workspace, "-p", prompt]
    proc = subprocess.run(full, check=False, text=True, capture_output=True)
    if proc.returncode != 0:
        raise SystemExit(proc.stderr.strip() or f"sigma exited {proc.returncode}")
    doc = json.loads(proc.stdout)
    return doc.get("text") or ""


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--instances", default="eval/swebench_lite_ids.txt")
    parser.add_argument("--limit", type=int, default=20)
    parser.add_argument("--output", default="eval/results/predictions.jsonl")
    parser.add_argument("--dataset", default="", help="optional JSONL of instances")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    if args.limit < 1 or args.limit > 50:
        raise SystemExit("--limit must be from 1 to 50")

    ids = ids_from(Path(args.instances), args.limit)
    if len(ids) < 1:
        raise SystemExit(f"no instance ids in {args.instances}")

    model = os.environ.get("SIGMA_MODEL", "")
    if not args.dry_run:
        if not os.environ.get("SIGMA_BASE_URL") or not model:
            raise SystemExit("set SIGMA_BASE_URL and SIGMA_MODEL")
        if not args.dataset:
            raise SystemExit("pass --dataset instances.jsonl (or use --dry-run)")
        data = load_dataset(Path(args.dataset))
        rows = []
        for instance_id in ids:
            if instance_id not in data:
                raise SystemExit(f"{instance_id} is not in {args.dataset}")
            patch = run_sigma(data[instance_id], model)
            rows.append(
                {
                    "instance_id": instance_id,
                    "model_name_or_path": model,
                    "model_patch": patch,
                }
            )
    else:
        rows = dry_predictions(ids, model)

    out = Path(args.output)
    out.parent.mkdir(parents=True, exist_ok=True)
    with out.open("w") as fh:
        for row in rows:
            fh.write(json.dumps(row) + "\n")
    print(f"wrote {len(rows)} predictions to {out}")


if __name__ == "__main__":
    main()
