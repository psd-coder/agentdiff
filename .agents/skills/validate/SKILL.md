---
name: validate
description: Run lint, format, vet, dead code and test checks on this Go repository
allowed-tools: [Bash]
---

# Validate

Lint and format (golangci-lint), `mygo vet`, `go mod tidy -diff`, deadcode and the tests of this repository. golangci-lint runs first, and the other steps run in parallel after it. Every step runs to completion even if another one fails.

`validate.sh` sits beside this file. Build its absolute path from this skill's own directory; it is `$SCRIPT` here. Run it yourself, in this session. It is one command with a terse result, so handing it to a subagent costs more than it saves.

```bash
bash $SCRIPT "$(pwd)" [flags]
```

| invocation | when |
| --- | --- |
| no flags | a task is done: every check, with the tests |
| `--quick` | iterating on edits; skips the tests (about 30 s) |
| `--pre-commit` | about to commit |
| `--pre-push` | about to push or open a PR |

`--pre-commit` and `--pre-push` name a **moment**, not a set of checks. Each one exits immediately when the repo's own git hook for that moment already runs these checks, and otherwise applies what that moment needs. Pass the moment and let the script decide. `--help` lists the rest.

## Reading the output

- **Pass**: a single `PASS` line. Relay it as-is, add nothing.
- **Fail**: a `FAIL` line, then each failing step's output verbatim. Relay the error lines, keep paths and line numbers exact.
- `test=skipped:quick` in the summary means the tests did not run. Never present such a run as a clean bill of health.
- `mode=fix` means golangci-lint applied its fixes (gofmt, modernize and other auto-fixes) and may have left modified files in the working tree. Mention it; do not revert it.
- A failing step longer than 100 lines is cut, with a line saying how many were dropped. Fix what is shown, then re-run to see the rest.
- `golangci-lint is not on PATH`: tell the user to install it (`brew install golangci-lint`), then stop.
