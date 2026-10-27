# Contributing

Thanks for helping. Bug reports, new skill checks, server and agent-loop adapters, and fixes are all welcome.

## Before your first pull request

You'll be asked to accept a short contributor license agreement ([CLA.md](CLA.md)) through the CLA Assistant bot, once. It lets HTMLGuy LLC keep licensing the project, including changing the license of future versions. You keep the copyright to your work.

## Making a change

1. Open an issue first for anything bigger than a small fix, so we can agree on the approach.
2. Match the style of the surrounding code: small functions, short doc comments, plain names.
3. Add tests for new behavior and run `gofmt -l . && go vet ./... && go test ./...` before you open the pull request.
4. Never add anything that sends a user's data off their machine, calls a paid API by default, or weakens the sandbox.

## Good first contributions

- A skill check for a new language in `internal/skills`.
- Quantization or context detection for another model server in `internal/servers`.
- Better grading for a job type in `internal/grade`, with real-world examples as tests.
