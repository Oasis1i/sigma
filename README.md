# Sigma

Sigma is a minimal coding-agent CLI written in Go. You give it a task, it calls an OpenAI-compatible model, and it reads and edits the workspace with a small set of tools. The loop is deliberately short: prompt, tool call, result, repeat.

It is a learning project. The interesting pieces (the agent loop, exact-string edits, approval rules, and the session log) are small enough to read in one sitting. Sandboxing (where a command runs) is separate from approval (whether it may run), so a Docker runner can be added later without rewriting the policy.

## 中文简介

Sigma 是一个用 Go 写的极简编码代理命令行工具。它通过 OpenAI 兼容接口调用模型，在本地工作区里读文件、搜索、精确替换、写入和执行 bash。

在 `~/.config/sigma/config.toml` 或项目里的 `.sigma.toml` 中设置 `base_url`、`model` 和 `api_key`。没有默认模型；缺少模型或地址时程序会直接退出并说明原因。环境变量 `SIGMA_BASE_URL`、`SIGMA_MODEL`、`SIGMA_API_KEY` 以及对应的命令行参数会覆盖配置文件。

- `sigma`：交互式 REPL
- `sigma -p "任务"`：执行一次后退出
- `sigma --resume <id>` / `sigma --continue`：从会话日志恢复

审批模式：`plan` 只允许只读工具；`ask` 在改文件或跑命令前询问（`y` / `n` / `always`）；`auto` 执行规则允许的操作。规则按拒绝、询问、允许三类判断，命中的拒绝优先于允许。会话以 JSONL 追加写入 `~/.local/share/sigma/sessions/`。

## Install

Requires Go 1.22 or newer.

```bash
git clone https://github.com/Oasis1i/sigma
cd sigma
go build -o sigma ./cmd/sigma
```

Or, once this module is available from the proxy:

```bash
go install github.com/Oasis1i/sigma/cmd/sigma@latest
```

## Configure

Copy [`config.example.toml`](config.example.toml) to `~/.config/sigma/config.toml` (or `$XDG_CONFIG_HOME/sigma/config.toml`) or to `.sigma.toml` in the workspace:

```toml
base_url = "https://api.openai.com/v1"
model = "gpt-4o-mini"
api_key = "sk-..."

approval_mode = "ask"   # plan | ask | auto
max_steps = 50
bash_timeout_seconds = 120

[[rules]]
action = "deny"
pattern = "bash:rm -rf *"

[[rules]]
action = "ask"
pattern = "bash:git push*"

[[rules]]
action = "allow"
pattern = "bash:go test*"
```

`base_url` is the API root that Sigma appends `/chat/completions` to. `https://host/v1` and a full `https://host/v1/chat/completions` URL both work. Any server that speaks the OpenAI chat-completions format is fine (the official API, a proxy, vLLM, llama.cpp).

Resolution order for scalar settings, later wins:

1. `~/.config/sigma/config.toml`
2. `.sigma.toml` in the workspace
3. `SIGMA_BASE_URL`, `SIGMA_MODEL`, `SIGMA_API_KEY`
4. flags: `--base-url`, `--model`, `--api-key`, `--mode`, `--max-steps`

`--config path` loads that file instead of (1) and (2). Rules from the user file and the workspace file are combined. An API key is optional so local servers can run without one; prefer `SIGMA_API_KEY` over `--api-key`, because flags show up in the process list.

There is no default model and no default base URL. If either is missing, Sigma exits and says where to set it.

Optional instruction files are appended to the system prompt: `~/.config/sigma/AGENTS.md`, then `AGENTS.md` at the git root (and at the workspace, if that is different). Each file is capped at 32KB.

## Usage

```bash
sigma                              # REPL
sigma -p "fix the failing test"    # one shot
sigma --mode plan -p "how does auth work?"
sigma --resume 20261111            # unique id prefix
sigma --continue                   # newest session
sigma --workspace ~/src/app -p "add a test"
```

Assistant text goes to stdout. The prompt, tool traces, and errors go to stderr, so `sigma -p "..." > answer.txt` keeps the reply.

In the REPL:

- `/mode plan|ask|auto` changes the mode for later tool calls
- `/session` prints the session id
- `/exit` or Ctrl-D leaves
- Ctrl-C interrupts the current turn; a second Ctrl-C exits

A turn stops when the model replies with no tool calls, or after `max_steps` model calls (default 50). Tool errors are fed back to the model so it can retry. Hitting the step limit is an error; in the REPL you can send another message and the model sees the tool results already recorded.

## Approval

| Mode | Behavior |
| --- | --- |
| `plan` | `read_file`, `list_dir`, and `grep` only. Writes and bash are denied even if an allow rule matches. |
| `ask` | Read-only tools run. `edit_file`, `write_file`, and `bash` prompt `y` / `n` / `always` unless a rule decides first. Without a terminal, the prompt is a deny. |
| `auto` | Anything not denied by a rule runs. This is what the eval runner uses. |

Rules match `tool` or `tool:glob`. `*` matches any text, including slashes, and patterns are anchored: `bash:rm -rf *` matches `rm -rf /tmp` and does not match `echo rm -rf /tmp`. `bash` with no colon matches every bash call.

They are not first-match-wins. Sigma collects matches and applies them in this order:

1. Any matching **deny**
2. Plan mode's read-only wall
3. Any matching **ask**
4. Any matching **allow**
5. A session "always" grant for that tool (from the `always` answer or a resumed log)
6. The mode default (read-only tools are allowed; other tools are asked in `ask` and allowed in `auto`)

"Always" does not override a deny rule or an explicit ask rule. It only stops the default prompt for that tool until the process exits. Resume restores those grants from the log.

Approval is not the sandbox. The policy decides whether a call may run. `internal/sandbox` decides where a bash command runs. Today that is a local process group in the workspace directory. A Docker runner would implement the same `Runner` interface.

## Tools

| Tool | What it does |
| --- | --- |
| `read_file` | Text file with `LINE\|CONTENT` numbers. `offset` is 1-based, `limit` defaults to 2000 lines. The number and the pipe are not part of the file. |
| `list_dir` | One directory, not recursive. |
| `grep` | Go regular expression. Skips `.git`, symlinks, binary files, files over 1MB, and paths ignored by a practical subset of `.gitignore`. Caps at 100 matches. |
| `edit_file` | Exact replacement. `old_string` must occur once. Zero matches or several matches is an error and the file is left untouched. Empty `new_string` deletes the match. |
| `write_file` | Create or overwrite a file, including parent directories. |
| `bash` | `bash -c` in the workspace, default timeout 120s. Exit code is included. Output keeps the first and last 16KB. A per-call timeout can only be shorter than the configured one. |

Paths must stay inside the workspace. A symlink that points outside is rejected.

If the model returns no native tool calls but writes a fenced `bash`, `sh`, or `shell` block, Sigma runs that block as `bash`. Other fences are ignored. The system prompt tells the model not to put examples in bash fences.

## Sessions

Each run appends JSONL under `~/.local/share/sigma/sessions/` (`$XDG_DATA_HOME/sigma/sessions` when that is set). Events are messages, tool calls, tool results, and approval decisions. `--resume` and `--continue` rebuild the conversation from `message` events. The API key is not written to the log.

## Layout

```
cmd/sigma            CLI, REPL, flags
internal/agent       the loop and the bash-fence fallback
internal/provider    OpenAI-compatible streaming client, plus a scripted fake
internal/tools       the six tools
internal/approval    deny / ask / allow policy
internal/sandbox     local process execution
internal/session     JSONL log
internal/config      file, environment, flags
internal/prompt      system prompt and AGENTS.md
evals                small tasks and check scripts
scripts/run-evals.sh runs those tasks; not used in CI
```

## Design notes

The provider is a short HTTP client for chat completions, not the generated OpenAI SDK. The request and the SSE stream are the whole protocol a compatible server implements, and the file is small enough to debug when a local model misbehaves. `Client` is the seam tests use: the agent tests run against a scripted provider with no network.

Edits follow the same idea as Claude Code and OpenCode: the model must quote a unique snippet. A fuzzy match would hide mistakes. When the snippet is missing only because of CRLF versus LF, the error says so.

The loop matches mini-swe-agent's shape (model, act, repeat, stop on a plain reply) and Codex's split between an approval policy and an execution sandbox. The sandbox package does not import the policy. File tools still touch the local disk; a remote sandbox needs a filesystem hook next to `Runner`.

Bash is not a login shell. The parent environment, including `PATH`, is inherited, but profile scripts are not sourced, because they often `cd` or define aliases. On timeout, Sigma kills the process group so a background `sleep` started by the script dies too. Output is a capped head plus tail so `yes` cannot grow memory without bound.

`.gitignore` handling is a subset: comments, negation, directory-only patterns, a leading slash, `*`, and `**`. The last match wins. A file inside a directory that was already skipped cannot be re-included, which is also how git behaves. `read_file` can still open an ignored file by path. Deny `read_file:.env` if that matters.

## Tests

```bash
go test ./...
go vet ./...
gofmt -l .
```

CI runs those three. Coverage is the edit uniqueness rule (including a symlink that points outside the workspace), bash timeout and head/tail truncation, approval ordering, session round-trip and resume, the agent loop against a fake provider (native tools, the bash-fence fallback, plan mode, deny rules, max steps), config merge order, and the HTTP client against a local stream.

## Evals

`evals/` holds 15 small tasks. Each has a prompt, a fixture under `workspace/` when one is needed, and a `check.sh`. The runner copies the fixture to a temp directory and runs Sigma one-shot in auto mode:

```bash
scripts/run-evals.sh
```

These tasks need a configured model, so they are not part of GitHub Actions.

## Roadmap

**v2**

- context compaction for long sessions
- git checkpoint and `/undo`
- lint and test feedback after edits
- a repo map (the Aider idea)
- a Bubble Tea TUI
- Docker as a `sandbox.Runner`

**v3**

- subagents and a richer plan mode
- an MCP client
- a SWE-bench Lite subset compared with mini-swe-agent

## Limitations

- Execution is local and POSIX. Process-group kill is Unix-specific.
- The conversation is not compacted. A long session will eventually exceed the model's context.
- "Always" is per tool name for the session, not per command.
- The `.gitignore` matcher is a subset, and named `read_file` calls can still read ignored files.
- A fenced bash block with no native tool call is executed, including an example the model did not mean to run. The system prompt warns it.
- Tool calls in one turn run one after another, so approval prompts stay in order.
- There is no TUI, no git checkpoint, and no Docker sandbox yet.

## License

[MIT](LICENSE)
