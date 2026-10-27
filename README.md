# Hometown Discount

**Find out which of your Claude Code work a local model can actually do, proven on your own jobs and your own code.**

*Stop paying out-of-state fees for work your hometown can handle.* Your local models get the hometown rate; Claude is the out-of-state trip.

> 🚧 **v0.1, an early preview.** It works end to end on macOS and Linux (see [Install](#install-and-set-up)). Expect rough edges.

Free and source-available ([license](#license)) · `htd` on the command line · $0 extra by default · nothing leaves your machine.

## What it does

- **`htd map`** shows where your Claude Code subagent spend goes, and what it would cost on cheaper models.
- **`htd bench`** checks your local model on exact tasks built from your own repo (find where something is defined, find the lines that mention a word, list what a file declares, read a JSON value, find which files mention something), at growing file sizes.
- **`htd replay`** has your local model redo past jobs Claude already finished, on the same code, and grades each answer against Claude's.
- **`htd card`** shows what the model has earned: what it can take now, what it's accurate at but too slow for, what it hasn't proven yet, and what it isn't good enough for.
- **`htd mcp`** gives Claude an `ask_local` tool, so Claude can hand your local model the small work it has proven, and nothing else.

Work goes local only when the model is **accurate and fast enough**: by default, at most 20% slower than Claude on the same kind and size of task. You can change that per task and size.

Grading is mechanical (no AI judges another AI), so the same answer always gets the same grade. Everything runs on your machine and costs $0.

## Works with your Claude subscription

- **No API key needed, and no Claude calls.** Replays and skill checks run only on your local model. Claude's past answers are read from files already on your disk.
- **Your Claude login is never touched** and `ANTHROPIC_API_KEY` is never set.
- **`ask_local` saves context:** when Claude hands a proven task to the local model, the file contents never enter Claude's window.

## Install and set up

Download it for your Mac or Linux machine (it picks the right file for your computer):

```bash
curl -fsSL "https://github.com/HTMLGuyLLC/HometownDiscount/releases/latest/download/htd_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz" | tar -xz htd
sudo mv htd /usr/local/bin/
```

Or pick a file on the [releases page](https://github.com/HTMLGuyLLC/HometownDiscount/releases). To check that a download was built from this repo by its release workflow, run `gh attestation verify <file>.tar.gz --repo HTMLGuyLLC/HometownDiscount` (or compare it with `SHA256SUMS`). If macOS blocks a file you downloaded in a browser, run `xattr -d com.apple.quarantine htd` once.

Or build from source (Go 1.25 or newer):

```bash
git clone https://github.com/HTMLGuyLLC/HometownDiscount && cd HometownDiscount
go build -o htd ./cmd/htd && sudo mv htd /usr/local/bin/
```

Then:

| Step | Command | Takes |
| --- | --- | --- |
| 1. Setup wizard: spend map, then your local model (URL + model name, any OpenAI-compatible server) | `htd setup` | 1 minute |
| 2. Where your subagent spend goes | `htd map` | seconds |
| 3. Skill checks on your code (run inside a repo) | `htd bench` | minutes; a few hours for a big repo |
| 4. Replays of your real past jobs | `htd replay` | minutes per job; run it overnight |
| 5. What the model has earned | `htd card` | instant |
| 6. Let Claude hand it proven work | `claude mcp add htd -- htd mcp` | once |

**Local servers that work:** MTPLX, Splish/Splash, Ollama, LM Studio, llama.cpp, vLLM, or anything serving `/v1/chat/completions`. The wizard checks the real context window (it warns about Ollama's 4K default), speed and tool-call reliability. A server that isn't on your machine or local network is refused unless you say yes, because your code would go there.

**Platforms:** macOS (Apple Silicon or Intel) and Linux. Replays need a sandbox: built into macOS; on Linux install bubblewrap (`sudo apt install bubblewrap`). On Windows, `htd map` works; replays don't yet.

## Configuration

Settings live in `~/.htd/config.toml` (written by `htd setup`, readable only by you). Every default costs $0 extra.

```toml
[[local]]
url = "http://127.0.0.1:8000"        # any server with an OpenAI-compatible /v1/chat/completions
model = "your-model-name"             # as your server lists it
quant = "auto"                        # detected from the server or model name; set it if htd can't tell
loop = "builtin"                      # the agent loop replays run in
# allow_remote = true                 # only if the server is off your machine and network on purpose
```

Results are kept per **model · quant · loop**. Changing any of them starts fresh; old results stay on the card.

**More than one model.** Add a `[[local]]` block for each, e.g. a big model next to a small, fast one, or several small ones that each do a few things well (`htd setup` offers to add more). Each model earns its own proof per task and size; `htd bench` and `htd replay` run every model in turn (`--model` runs just one), and a model whose server is down is skipped. `ask_local` hands each task to the fastest model that has proven it and is within your speed limit. List smaller models first: a tie goes to the one listed first. To choose differently:

```toml
[pick]
prefer = "fastest"           # or "listed": the first model in your list that has proven the task
[pick.by_task]               # "skill:size" beats "skill"; used only while that model is proven and fast enough
"lookup" = "your-small-model"
"lines:large" = "your-big-model"
```

Skills: `json`, `lookup`, `lines`, `list`, `files`. A pinned model must be one in your `[[local]]` list. All models share one lock, so only one runs at a time: their timings stay fair, and two models never fill your memory at once.

**Speed limits.** A task the model has proven goes local only if it's at most `max_slower` percent slower than Claude. Set a different limit for a task, or a task at one size; the most specific setting wins:

```toml
[speed]
max_slower = 20              # default for everything; -1 turns the speed check off
claude_model = "auto"        # compare with the Claude model you used most in the last 3 days, or e.g. "claude-opus-5-5"

[speed.by_task]              # "task:size" beats "task" beats the default
"lookup:small" = 5           # small lookups: within 5% of Claude
"review:large" = 25          # big reviews: up to 25% slower is fine
"files:large" = "never"      # Claude always does it
"json" = "always"            # skip the speed check (it still has to be proven accurate)
```

Tasks: `json`, `lookup`, `lines`, `list`, `files` (skill checks) and `find`, `extract`, `classify`, `transform`, `review`, `research` (replays). Sizes: `small` (up to 200 lines), `medium` (up to 1,000), `large` (up to 4,000). A typo stops htd with a message instead of being ignored. `ask_local` picks up changes on its next call.

Claude's side of the comparison costs nothing: for skill checks, htd times how long that Claude model took to answer after reading a file that size in your own transcripts (re-measured weekly, and whenever you switch models); for replays, it uses how long the original job took. Until there are at least 5 timings on both sides, accuracy alone decides.

**Results are tied to the Claude model too.** A replay is graded against the answer one Claude model wrote, so replay results are kept per Claude model: the card shows "review jobs vs Opus 5.5" separately from "vs Sonnet 5", since matching one doesn't prove a match for the other. Skill checks have exact answers, so they don't depend on Claude.

## Commands

| Command | What it does |
| --- | --- |
| `htd setup` | First-run wizard: spend map, your local models, then snapshots |
| `htd map [--days 30] [--json]` | Spend map: jobs, cost and cheaper-model prices per job type |
| `htd bench [--repo .] [--max 150] [--timeout 5m]` | Skill checks from a repo on each local model, easiest first; stops early when a size clearly can't pass |
| `htd replay [--max 40] [--types find,review] [--days 30]` | Replay past jobs on each local model; resumable (done jobs are skipped) |
| `htd card [--details]` | What each model has earned; `--details` shows every number |
| `htd mcp` | MCP server with the `ask_local` tool |
| `htd probe --url URL` | Check a server: models, context, quant, speed, tool calls |
| `htd gold [--fidelity N]` | How many past jobs can be replayed |
| `htd regrade [--apply]` | Re-grade saved answers after an update, without re-running the model; `--apply` saves the new grades |
| `htd hooks [on\|off]` | Snapshots: save your repo's uncommitted files each time Claude starts a subagent, so replays see what each job saw (a Claude Code hook) |
| `htd forget [--all] [--yes]` | Delete what htd stored in `~/.htd` after asking (`--all` also deletes your settings and htd's hook) |

`bench` and `replay` also take `--url`, `--model` and `--allow-remote` to run one model instead of every configured one (`--max` applies to each model). Every command takes `--help`.

**What's stored, all in `~/.htd`:** `config.toml`, `runs.jsonl` (one line per graded replay or check), `answers/` (each answer next to the expected one, for review by hand), `delegations.jsonl` (each `ask_local` call: skill, size, outcome; no file contents), `cache/` (Claude's typical times: medians only, no transcript content), `snapshots/` (uncommitted files saved as subagents start, when snapshots are on; kept 60 days). See [PRIVACY.md](PRIVACY.md).

## Reading the card

- **proven**: passed often enough, with enough checks, that the result isn't luck (a 95% lower bound at or above the bar);
- **too slow**: proven, but slower than your speed limit allows, so Claude keeps it (`--details` shows how much slower);
- **learning** ("not proven yet"): not enough evidence either way yet;
- **not good enough**: stopped early, because it clearly can't reach the bar;
- **hint**: 200 jobs without proof.

The `ask_local` line counts tasks answered locally and the file contents kept out of Claude's context, with what that's worth at your Claude model's API prices: one cache write plus a cache read on each later turn the file would have ridden along for (typical for your transcripts). On a Pro or Max plan that's usage headroom, not dollars.

Skill checks have exact answers and a fixed bar of 85%. Replay results are a first look: their bar is a stated default (86%) for now. Run `htd bench` again after changing your server or model settings.

## Good to know

- **Big jobs fail, small work passes.** Common for local models: whole subagent jobs can be too big, while bounded pieces (lookups, lists, scans) are well within reach. That's what skill checks and `ask_local` are for.
- **Some past jobs can't be replayed**, for example ones that edited files, used a browser, ran tests, read files that were never committed, or whose repo is gone. They're skipped, never failed. `htd gold` shows how many can be replayed. With snapshots on (`htd hooks on`), jobs from then on that read uncommitted files can be replayed too.
- **Thinking models** (`<think>…</think>`, `reasoning_content`) work; reasoning is stripped before grading. A model that thinks before it answers is often far quicker with thinking off, and still right on simpler skills, so `htd bench` checks it both ways (thinking off first) and `ask_local` uses whichever way is proven and faster for each skill and size. The card shows the two as separate setups. Replays use your server's own setting.
- **Slow local models.** Each replay has a 20-minute limit and each skill check 5 minutes; a timeout counts as a fail. `htd replay` and `htd bench` keep your Mac awake while they run.
- **Server problems aren't counted against the model.** A crash or restart is waited out and that job is skipped.
- **Low memory pauses a run.** Before each job or check, `htd replay` and `htd bench` check your machine's memory; when it's low they wait up to 10 minutes for it to recover, then stop (finished work is kept). A local model server that grows with use is the usual cause: restart it or lower its memory limit.
- **Two htd commands at once** take turns on the local model, one request at a time. An `ask_local` call from Claude goes first; if the model stays busy, Claude is told so and does the task itself.
- **Cloud models served through a local URL** (e.g. Ollama `:cloud`) are refused: they aren't local.

## Links

[Privacy](PRIVACY.md) · [Security](SECURITY.md) · [Terms of use](TERMS.md) · [Contributing](CONTRIBUTING.md)

"Claude" and "Claude Code" are trademarks of Anthropic. Hometown Discount is an independent project and is not affiliated with or endorsed by Anthropic or the makers of any local server.

## License

**Hometown Discount is free and source-available under [PolyForm Shield 1.0.0](https://polyformproject.org/licenses/shield/1.0.0).** This license is not "open source" in the OSI sense.

**You can:**
- use it for anything, including at work and for your own commercial projects
- read, modify and build on the code
- share copies, changed or not, as long as the license and the notice below go with them

**You can't:**
- offer a product or service that competes with Hometown Discount, whether it's paid or free
- rebrand it, or a version you built from it, and offer it as your own competing tool

Every copy must keep this line:

```text
Required Notice: Copyright (c) 2026 HTMLGuy LLC (https://github.com/HTMLGuyLLC/HometownDiscount)
```

**Name and logo:** forks and copies may not use the Hometown Discount name or logo (see [TRADEMARKS.md](TRADEMARKS.md)).

**Contributing:** contributions are welcome. A one-time, plain-English contributor agreement is signed through the CLA Assistant bot on your first pull request (see [CONTRIBUTING.md](CONTRIBUTING.md)).
