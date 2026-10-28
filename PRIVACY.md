# Privacy

Hometown Discount runs on your computer and talks only to the model servers you set up. It has no servers of its own, no accounts, no analytics and no telemetry.

## What it reads

- **Your Claude Code transcripts** in `~/.claude/projects` (or `$CLAUDE_CONFIG_DIR/projects`): to build the spend map, to find past jobs to replay, and to time how long Claude takes to answer (for the speed limits). They're read, never changed.
- **Your repos**, at the commits your past jobs saw: to rebuild a copy for each replay and to generate skill checks. With snapshots on, also their uncommitted files, each time Claude starts a subagent.
- **Your local model servers**, at the URLs you give it.

## What it changes outside `~/.htd`

- **Claude Code's `settings.json`**, only to add or remove htd's snapshot hook when you turn snapshots on or off (`htd hooks on|off`, or `htd setup`). Everything else in the file is kept as written.

## What it sends, and where

- **To your local model servers only:** the tasks from your past jobs, the code the model asks to see during a replay, skill-check prompts built from your files, and the files Claude passes to `ask_local`.
- **Nowhere else.** Nothing is sent to Anthropic, to the project's author or to any third party. A model server that isn't on your machine or local network is refused unless you explicitly allow it, because your code would go there.

## What it stores

Everything lives in `~/.htd` (or `$HTD_HOME`), readable only by your user account:

| File | Contents |
| --- | --- |
| `config.toml` | Your local models' URLs and names, and your settings |
| `runs.jsonl` | One line per graded replay or skill check: ids, job type, pass/fail, timing. No prompts; a failed replay's notes can name file paths or identifiers it got wrong. |
| `answers/` | Each answer next to the expected one (from Claude, or computed for skill checks), so you can check the grading by hand. These can contain code and text from your projects. |
| `delegations.jsonl` | One line per `ask_local` call: skill, size, outcome, timing. No file contents. |
| `cache/` | Temporary repo copies during a replay, deleted when it ends; `claude-speed.json`, Claude's median answer times by file size (numbers only); `thinking.json`, whether each model setup thinks before answering (true/false, checked again weekly) |
| `model.lock`, `model.want` | Empty files htd commands use to take turns on the model |
| `snapshots/` | Only when snapshots are on (`htd hooks on`, offered by `htd setup`): your repos' uncommitted text files, saved each time Claude starts a subagent so a replay can rebuild what the job saw. Files that look like secrets (the same list as below), files git ignores, binary files and files over 1 MB are left out. Kept 60 days. |

Delete it all with `htd forget --all` (the two empty turn-taking files stay, in case another htd command is running), or everything except your settings with `htd forget`. `htd hooks off` stops new snapshots; `htd forget --all` also removes htd's hook from Claude Code's settings.

## Secrets

- `htd` never reads, stores or sends your Claude login or API keys.
- Replayed commands run in a sandbox with no network and no access to your home folder outside the replay's copy of the repo, so a model can't read `~/.ssh`, `~/.aws` or your other projects.
- Files that usually hold secrets (`.env*`, keys, credentials, `.git/`, `.ssh/`) are never used for skill checks or read by `ask_local`.
- Past jobs can include secrets that appeared in your transcripts or repos. Those can reach your local model during a replay, the same as they reached Claude originally. They go no further than your model server.
