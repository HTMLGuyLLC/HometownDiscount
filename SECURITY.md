# Security

## Reporting a vulnerability

Please report security issues privately through GitHub's **Report a vulnerability** button on this repository (Security → Advisories), not in a public issue. You'll get an answer within a few days.

## How htd limits risk

- **Sandboxed replays.** Commands a local model runs during a replay go through macOS Seatbelt or Linux bubblewrap: the repo copy is read-only, writes go only to a scratch folder, there's no network, no access to your home folder or other temp folders, no terminal, no clipboard or app launching, no looking at or signalling other processes, a clean environment, and time, output and file-size limits. `go test ./internal/sandbox/` runs canary tests for each rule.
- **Contained file access.** The replay file reader and `ask_local` open files through Go's `os.Root`, which refuses paths that leave the folder, including through symlinks.
- **Nothing remote by default.** `htd` talks only to the model server you configure, refuses one that isn't on your machine or local network unless you allow it, and refuses cloud models served through a local URL.
- **Secrets files are skipped.** `.env*`, keys, credentials, `.git/` and `.ssh/` are never sent to the model by skill checks or `ask_local`, including through symlinks.
- **Local answers are labeled.** `ask_local` returns the local model's answer as data from another program, checked for the shape its skill expects, so a project file can't easily steer Claude through it.
- **No credentials.** `htd` never handles your Claude login or API keys.
