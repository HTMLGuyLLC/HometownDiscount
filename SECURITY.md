# Security

## Reporting a vulnerability

Please report security issues privately through GitHub's **Report a vulnerability** button on this repository (Security → Advisories), not in a public issue. You'll get an answer within a few days.

## How htd limits risk

- **Sandboxed replays.** Commands a local model runs during a replay go through macOS Seatbelt or Linux bubblewrap: the repo copy is read-only, writes go only to a scratch folder (capped at 512 MB), there's no network, no access to your home folder or other temp folders, no terminal, no clipboard or app launching, no looking at or signalling other processes, no shared memory, a clean environment, and time, output, file-size and process-count limits. Nothing a command starts outlives it: on Linux the sandbox has its own process namespace; on macOS leaving the process group is blocked, and a process detached another way (`posix_spawn`'s setsid flag) is found by its working folder and stopped. One that also changes folder stays inside the sandbox, with nothing to write once the job's folders are deleted. On Linux the sandbox sees only the system's program, library and settings folders, so daemon sockets in `/var` and `/run` don't exist inside it. `go test ./internal/sandbox/` runs canary tests for each rule.
- **Contained file access.** The replay file reader and `ask_local` open files through Go's `os.Root`, which refuses paths that leave the folder, including through symlinks. `ask_local` won't start in your home folder, a folder above it, or a hidden folder in it such as `~/.claude`.
- **Nothing remote by default.** `htd` talks only to the model server you configure, refuses one that isn't on your machine or local network unless you allow it (a short or `.local` host name counts as local only if every address it resolves to is, checked again each time a connection opens, so a name can't be pointed elsewhere after the check), and refuses cloud models served through a local URL. It never uses a proxy for the model server and never follows a redirect to another host.
- **Secrets files are skipped.** `.env*`, keys, credentials (including Claude's own), `.git/`, `.ssh/`, `.aws/`, `.kube/`, `.gnupg/`, Docker and GitHub CLI tokens, and shell histories are never sent to the model by skill checks or `ask_local`, including through symlinks, link chains and linked folders.
- **Local answers are labeled.** `ask_local` returns the local model's answer as data from another program, checked for the shape its skill expects, so a project file can't easily steer Claude through it.
- **No credentials.** `htd` never handles your Claude login or API keys.
- **Verifiable downloads.** Release archives come with signed build provenance from the release workflow (check with `gh attestation verify`) and a `SHA256SUMS` file; the workflow's actions are pinned to exact commits.
