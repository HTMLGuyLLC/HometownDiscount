# Personas

Last reviewed: 2026-11-19

## Customers

### Claude Code subscriber with a Mac and a local model
- **Who:** Uses Claude Code on a subscription and runs a local model server (MTPLX, Ollama, LM Studio).
- **Goal:** Find out which of their Claude work a local model can really take, then let it.
- **Context:** Developer, macOS, comfortable with a terminal, short on time.
- **Needs:** Setup in minutes, clear proof per task and file size, a plain view of what was saved.
- **Let down by:** Vague scores, numbers with no explanation, setup that needs config editing.
- **Areas:** setup wizard, htd bench, htd card, htd ui, ask_local
- **View as:** One local model on 127.0.0.1, a few days of bench results

### API-key / pay-per-token user
- **Who:** Pays Claude per token and watches the bill.
- **Goal:** Know how many dollars local models save now and could save.
- **Context:** Developer or small-team lead, cost-focused.
- **Needs:** Dollar figures they can trust, how they were worked out, monthly estimates.
- **Let down by:** Savings with no basis, estimates presented as fact.
- **Areas:** htd map, htd card, htd ui Savings tab, pricing
- **View as:** Delegations logged over several weeks

### Power user / tinkerer
- **Who:** Runs several local models and tunes which one takes what.
- **Goal:** Fine control: per-task pins, speed limits per task and size.
- **Context:** Expert, edits config by hand or with an AI assistant, uses the CLI.
- **Needs:** Exact numbers, every setting reachable, hand edits respected.
- **Let down by:** A form that drops settings or comments, hidden behavior.
- **Areas:** config.toml, htd ui Settings, pick.by_task, speed.by_task
- **View as:** Two or more [[local]] models, pins and limits set

### Model on another machine on the network
- **Who:** Runs the model on a separate box (a GPU server or another Mac) on their LAN.
- **Goal:** Use that machine safely.
- **Context:** Knows networking, cares where code goes.
- **Needs:** Clear warning before code leaves the computer, allow_remote that works.
- **Let down by:** Silent sending, or a refusal with no way past it.
- **Areas:** config allow_remote, setup wizard, htd ui Settings
- **View as:** [[local]] url on a 10.x or 192.168.x address

### Privacy-conscious developer
- **Who:** Works on private code and reads PRIVACY.md before installing.
- **Goal:** Know exactly what is stored and sent, and turn it off.
- **Context:** Skeptical, reads docs closely.
- **Needs:** No file contents logged, an off switch for tracking, htd forget, local-only UI.
- **Let down by:** Logs that store more than documented, a UI reachable from other sites.
- **Areas:** delegations.jsonl, privacy.track_handoffs, htd ui server, PRIVACY.md
- **View as:** track_handoffs = false

## Situations

### Non-technical user
- **Who:** Can follow install steps but isn't comfortable with config files or TOML.
- **Goal:** Get set up and change settings without editing files.
- **Context:** Low confidence, easily overwhelmed.
- **Needs:** Plain words, a settings form that can't save a broken file, a clear next step.
- **Let down by:** Jargon, raw config errors, dense tables.
- **Areas:** htd ui (all tabs), README
- **View as:** Defaults, little data

### First-time user with no data yet
- **Who:** Just installed htd and opened htd ui.
- **Goal:** Understand what the page will show and what to run first.
- **Context:** Curious, hasn't run bench or replay.
- **Needs:** Helpful empty states that name the next command.
- **Let down by:** A blank or broken-looking page, zeros with no explanation.
- **Areas:** htd ui empty states
- **View as:** Empty ~/.htd

### User whose setup broke
- **Who:** Has a broken config.toml, a stopped model server, or refused handoffs.
- **Goal:** Find out what's wrong and fix it.
- **Context:** Frustrated, wants the fix, not a trace.
- **Needs:** A plain reason and the fix, no lost settings.
- **Let down by:** Silent failure, a page that won't load, a form that wipes their file.
- **Areas:** config load errors, htd ui Settings, ask_local refusals
- **View as:** config.toml with a typo; server down

### Linux user
- **Who:** Runs htd on Linux.
- **Goal:** Same experience as on macOS.
- **Context:** Developer, maybe headless or over SSH.
- **Needs:** The browser opens (or a URL is printed), bubblewrap note for replays.
- **Let down by:** Mac-only assumptions.
- **Areas:** htd ui --no-open, xdg-open, install
- **View as:** Linux, maybe no desktop

### Keyboard, screen-reader or low-vision user
- **Who:** Uses only a keyboard, a screen reader, or high zoom.
- **Goal:** Read results and change settings.
- **Context:** Relies on semantics and contrast.
- **Needs:** Accessible tabs and tables, results not shown by color alone, contrast in both themes.
- **Let down by:** Click-only cells, color-only status, unlabeled charts.
- **Areas:** htd ui
- **View as:** Keyboard only; dark mode; 200% zoom

### Skeptic checking the claims
- **Who:** Doubts that local models can save anything.
- **Goal:** Find out whether the numbers are honest.
- **Context:** Reads the method, looks for overreach.
- **Needs:** Mechanical grading, estimates clearly labeled with their basis.
- **Let down by:** Hype, estimates shown as facts, unexplained totals.
- **Areas:** README, htd ui Savings, how-it-works diagram
- **View as:** Any

## Team

### Maintainer
- **Who:** The project owner, who handles issues, releases and support alone.
- **Goal:** Keep htd easy to change and cheap to support.
- **Context:** Small team of one, plus AI assistants.
- **Needs:** Simple code, few support questions, bug reports with useful detail, docs that stay true.
- **Let down by:** Parallel implementations, docs drifting from behavior, features that need upkeep.
- **Areas:** all code, README, PRIVACY.md, SECURITY.md, CI
- **View as:** Repo checkout
