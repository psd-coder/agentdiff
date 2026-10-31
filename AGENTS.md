# AGENTS.md

AgentDiff is a native macOS app in Go, built on [MyGo](https://mygo.egoist.dev) (`github.com/egoist/mygo`, pinned in `go.mod`, drawn with Metal, no webview). It runs the `claude` or `codex` CLI read-only to write a walkthrough of a git diff, then collects review comments into a Markdown feedback file. `README.md` is the user-facing spec: features, storage layout, keyboard shortcuts. `CONTEXT.md` defines the domain terms.

## Commands

- `go run . /path/to/repo`: start the app on a repository.
- `go tool mygo dev`: development build that restarts on every change, opened on this repository.
- `go tool mygo build`: `AgentDiff.app` and a `.dmg` in `build/darwin-<arch>/`.
- `CGO_ENABLED=0 go test ./...`: all tests, with no network and no agent calls. One test: `go test -run TestName ./internal/app`.
- `go tool mygo vet ./internal/app`: go vet plus checks for UI elements kept past their build pass and goroutine captures.
- `golangci-lint run ./...`: the linters and gofmt, configured in `.golangci.yml`.
- `AGENTDIFF_SNAPSHOTS=<existing dir> go test ./internal/app`: also writes headless PNG screenshots of the main screens.
- `AGENTDIFF_E2E=claude|codex go test -run TestEndToEnd -v ./internal/app`: real agent run on the user's subscription. Run it only when the user asks.

Release: set `version` in `mygo.json`, commit, push tag `v<version>`. `.github/workflows/release.yml` fails when tag and version differ.

## Validate

The `validate` skill (`.agents/skills/validate/validate.sh`) runs golangci-lint with its fixes, `mygo vet`, `go mod tidy -diff`, deadcode and the tests:

- While iterating on code changes, run it with `--quick`. It skips the tests, so it takes seconds.
- Before you say a task is done, run it with no flags.
- Before a commit, pass `--pre-commit`. It only reports and never rewrites staged files.

## Architecture

- `main.go` picks the repository and calls `app.Run`.
- `internal/app` holds the UI and app state in one `app` struct (`app.go`). `view*.go` has one file per screen. `run.go` starts the agent runs: overview, deeper walkthroughs, questions, threads. `runner.go` holds the `runner` type: every run, keyed by its review and its target, the queue of the deeper walkthroughs the app writes ahead, and the one path through which a run ends. The app gives it `a.stream`, which runs the agent and writes the run log, and `a.saveReviewOf`. Its tests use a fake stream and need no git repository. The run logs in memory are the `logs` field of `app`. `target.go` gives the target state that views and commands show: the run in the runner or the outcome the review saved, and whether the target is unseen. `scopeload.go` holds the `scopeLoader` type: it starts a load of a scope, reads the changes on a goroutine with `readScope`, runs the phases of the continuity load, and drops the results of older loads. It hands the changes, the commit that holds them and each file read to the app through the `loadSink` interface, which `app` implements. Its tests use a recording sink and the fixture repository. `scopeload.go` also gives the freshness of the overview. `command.go` and `keys.go` hold the commands that the menu bar, the shortcuts and the command palette share. `watch.go` follows the repository's files and git directory.
- `internal/agent` builds the CLI arguments for each provider, parses the JSONL stream into `Event`s, writes run logs, and reads `PATH` from the login shell. `outcome.go` reads from a run's log items how the run ended, what it spent and its own cost. `ResumedSession` reads the resumed session back from a logged command. `testdata/` holds recorded CLI output that the parse tests replay.
- `internal/gitx` wraps the git CLI. `Scope` is uncommitted changes, a branch against a base, one commit, or a range. A load reads its changes, diffstat, fingerprint and file contents from one `Snapshot`, which resolves the scope to fixed commits once. `patchid.go` gives the `git patch-id` fingerprints that let a review follow its changes across commit, amend, rebase and stash.
- `internal/review` holds the review model (`Review`, `Walk`, `Thread`, `Anchor`), builds the prompts, parses the agent's Markdown answer into steps and file groups, and exports the feedback.
- `internal/continuity` lets a review follow its changes: `Earlier` is an archived review with the commit that now holds its changes, and `Reviews` keeps the open review of each scope, the earlier reviews and the restored ones. Each load of a scope gets a `Load` from `Reviews.Begin` and calls its phases in order: `Begin` and `Decide` on the UI thread, `Read` and `Locate` on the load goroutine. `Read` reads the store. `Decide` takes over a saved review written for the same changes, or it archives the review of the uncommitted changes. `Locate` looks for the commits that now hold the changes of the earlier reviews, and the function it returns puts them into the list on the UI thread. `Of` and `Rescope` give a review another scope that names the same changes. `Obsolete` tells a late run result that its review was replaced.
- `internal/store` keeps JSON under `~/Library/Application Support/AgentDiff/`: config, one review per repo and scope, run logs, the archive of earlier reviews.

Threading: the views never block. Git and agent work runs on goroutines and returns through `a.update(fn)`, which calls `Window.Update` in the app and sends to `a.queue` in tests. Every run belongs to the `*review.Review` it started for, so a run keeps going when the user switches scope. Results check the generation counters (of the scope loader in `scopeload.go` and of the commit list in `commitlist.go`) and drop stale data.

Tests in `internal/app` drive a window-less app: `newTestApp` creates a fixture repo and a temp store, and `ui.NewTester` renders the views headless. Every agent run starts through `app.startAgent`, which is `agent.Run` in the app. Tests replace it with `newFakeAgent` (`fakeagent_test.go`). The fake answers in process and records the requests. It can hold a run until the test releases it or the run is cancelled. `TestAgentLog` runs a real process.

## MyGo rules

Before a UI change, a crash fix or a MyGo upgrade, invoke the `mygo-maintenance` skill.

The GPU renderer only rounds the innermost clip, and `ui.Tester` rounds all clips, so tests do not show the bug. A rounded container that holds a scroll view or a diff view paints its corners on top (see `cardCorners` in `view_files.go`).
