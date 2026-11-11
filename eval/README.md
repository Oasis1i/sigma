# Sigma evals

Local tasks and a SWE-bench Lite driver. Nothing here calls a model unless you set `SIGMA_BASE_URL` and `SIGMA_MODEL`. Results go to `eval/results/`, which is gitignored.

## Local tasks

`eval/tasks/` has 15 small repos. Each has `prompt.txt`, an optional `repo/`, and `check.sh`. The check is not copied into the workspace, so the tests stay hidden from the agent. `check.sh` receives `WORKSPACE`.

```bash
# layout only, no model
eval/run.sh --dry-run

export SIGMA_BASE_URL=https://api.openai.com/v1
export SIGMA_MODEL=gpt-4o-mini
export SIGMA_API_KEY=sk-...          # omit for a local server
eval/run.sh
```

`SIGMA_BIN` overrides the binary. Otherwise the script runs `go run ./cmd/sigma`. Sigma is invoked as `sigma --mode auto --output json -p ...`. The script prints:

- `pass_rate` — checks that passed
- `steps` — model calls, summed
- `tokens` — prompt plus completion tokens, summed, when the server reports usage
- `edit_failure_rate` — failed `edit_file` / `write_file` calls over attempts

Per-task JSON is written under `eval/results/`. Do not commit that directory.

## Headless output

```bash
sigma --mode auto --output json -p "fix the test"
sigma --mode auto --output jsonl -p "fix the test"
```

`json` is one object: `text`, `steps`, `prompt_tokens`, `completion_tokens`, `tool_calls`, `edit_attempts`, `edit_failures`, `error`, and `events`. `jsonl` is one engine event per line. Use `--mode auto` so a headless run is not blocked on an approval prompt.

## SWE-bench Lite

`eval/swebench_lite_ids.txt` lists 30 public Lite ids. `--limit` is 1 to 50.

```bash
python3 eval/swebench.py --dry-run --limit 2 --output /tmp/predictions.jsonl
```

That writes the official predictions schema (`instance_id`, `model_name_or_path`, `model_patch`) with an empty patch and does not call a model.

A real run needs a JSONL dataset you already have (one object per line, with `instance_id` and `problem_statement`, and optional `workdir`):

```bash
export SIGMA_BASE_URL=... SIGMA_MODEL=... SIGMA_API_KEY=...
python3 eval/swebench.py --limit 20 --dataset instances.jsonl \
  --output eval/results/predictions.jsonl
```

Feed `predictions.jsonl` to the official SWE-bench Docker harness. This repo does not run that harness.

## mini-swe-agent baseline

Optional, not installed here.

```bash
python3 eval/baseline.py --dry-run --limit 2
pip install mini-swe-agent
```

The dry run prints the command shape for the same ids and the same `SIGMA_MODEL` / `SIGMA_BASE_URL`. It does not import the package.

## 中文

本地任务在 `eval/tasks/`，检查脚本不会放进工作区。没有密钥时用 `eval/run.sh --dry-run` 只检查题目是否齐全。真正跑需要环境变量 `SIGMA_BASE_URL` 和 `SIGMA_MODEL`，密钥是 `SIGMA_API_KEY`。`eval/results/` 不要提交。

`sigma -p --output json` 打出一整份 JSON；`--output jsonl` 一行一个事件。SWE-bench 用 `python3 eval/swebench.py --dry-run --limit 2` 写出 `predictions.jsonl` 的格式，不调用模型。正式评测再把该文件交给官方 Docker harness。
