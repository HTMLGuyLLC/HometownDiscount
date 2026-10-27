# Privacy

Hometown Discount runs entirely on your computer. It has no servers, no accounts, no analytics and no telemetry.

## What it reads

- **Your Claude Code transcripts** in `~/.claude/projects` (or `$CLAUDE_CONFIG_DIR/projects`): to build the spend map and to find past jobs to replay. They're read, never changed.
- **Your repos**, at the commits your past jobs saw: to rebuild a copy for each replay and to generate skill checks.
- **Your local model server**, at the URL you give it.

## What it sends, and where

- **To your local model server only:** the tasks from your past jobs, the code the model asks to see during a replay, skill-check prompts built from your files, and the files Claude passes to `ask_local`.
- **Nowhere else.** Nothing is sent to Anthropic, to the project's author or to any third party. A model server that isn't on your machine or local network is refused unless you explicitly allow it, because your code would go there.

## What it stores

Everything lives in `~/.htd` (or `$HTD_HOME`), readable only by your user account:

| File | Contents |
| --- | --- |
| `config.toml` | Your local model's URL and name, and your settings |
| `runs.jsonl` | One line per graded replay or skill check: ids, job type, pass/fail, timing. No prompts; a failed replay's notes can name file paths or identifiers it got wrong. |
| `answers/` | Each answer next to the expected one (from Claude, or computed for skill checks), so you can check the grading by hand. These can contain code and text from your projects. |
| `delegations.jsonl` | One line per `ask_local` call: skill, size, outcome, timing. No file contents. |
| `cache/` | Temporary repo copies during a replay, deleted when it ends |

Delete it all with `htd forget --all`, or everything except your settings with `htd forget`.

## Secrets

- `htd` never reads, stores or sends your Claude login or API keys.
- Replayed commands run in a sandbox with no network and no access to your home folder outside the replay's copy of the repo, so a model can't read `~/.ssh`, `~/.aws` or your other projects.
- Files that usually hold secrets (`.env*`, keys, credentials, `.git/`, `.ssh/`) are never used for skill checks or read by `ask_local`.
- Past jobs can include secrets that appeared in your transcripts or repos. Those can reach your local model during a replay, the same as they reached Claude originally. They never leave your machine unless your model server is remote.
