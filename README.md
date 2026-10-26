# agentdiff

A native macOS app for reviewing the code your coding agent wrote. You read the agent's explanation first and the diff grouped by intent after it. Then you hand a feedback file back to the agent.

- **Explanation by your own agent**: Claude Code or Codex reads the changes through its CLI on your subscription. It answers six review questions that you can edit.
- **Files grouped by intent**: the agent puts every changed file into a group, in the order you should read them. Without an explanation, files are grouped by directory.
- **Any scope**: uncommitted changes (untracked files included), your branch against its base, a single commit from the history list, or a custom `from..to` range.
- **Comments anywhere**: click `+` in the gutter, select line numbers, comment on a whole file or on one part of the explanation.
- **Separate discussion threads**: the first question of a thread forks the agent session that wrote the explanation, so it starts with that context. Later questions go on in the thread's own session, and threads never mix.
- **Feedback for the agent**: one Markdown document with a `path:line (+)` heading per comment, the quoted code, and the agent's answers if you want them. Copy it, or save it and copy a prompt that points to the file.
- **Agent log**: every run of an agent CLI is logged, with the command line, the prompt, each tool call and its output, token counts, and how the run ended. The log panel opens on the right, for all runs of the review or for the overview or one thread.
- **Read-only agents**: Codex runs in its `read-only` sandbox. Claude runs in `dontAsk` mode with an allow list of read commands (`git diff`, `git show`, `grep`, `cat` and a few more), so any other Bash call is refused.
- **Settings for all reviews and per review**: agent, model, binary path, extra CLI arguments, review questions.
- **Native UI**: built with [MyGo](https://mygo.egoist.dev) in Go, drawn with Metal, no webview. The app bundle is 10.9 MB.

## Getting started

You need macOS 12 or later, Go 1.27.3 or later and git. You also need at least one of the agent CLIs, installed and signed in:

- [Claude Code](https://docs.claude.com/en/docs/claude-code) (`claude`), tested with 2.1.310
- [Codex](https://github.com/openai/codex) (`codex`), tested with 0.162.1

The app reads the `PATH` of your login shell (`$SHELL -l -i`), so it finds the CLIs when you start it from the Dock too. Settings shows the version of each CLI it found, or why it found none.

Build the app. The first build downloads MyGo through the `tool` line in `go.mod`.

```bash
go tool mygo build
```

It worked when `build/darwin-<arch>/` holds `AgentDiff.app` and a `.dmg`, with `arm64` or `amd64` for `<arch>`. Open a repository from the app with Cmd+O, or start it on the repository you are in from a terminal:

```bash
open -n build/darwin-arm64/AgentDiff.app --args "$PWD"
```

During development, `go run . /path/to/repo` starts the app on a repository. Without an argument the app opens the current directory when that is a git repository, and shows a welcome screen otherwise.

## Core concepts

An agent changes 40 files in ten minutes. A pull request lists them in path order, which scatters related changes across the list. You lose track of why each one exists. `agentdiff` asks the agent to explain the change set first, with these default questions:

1. Explain on high level.
2. Explain data changes.
3. Explain UI changes and how to see them.
4. Explain architecture and API changes.
5. Explain non-beautiful changes.
6. Tell about assumptions and rules which force non-beautiful changes.

Then you read the files group by group and leave comments where you disagree.

In `agentdiff`, a question to the agent and a note for the agent are both a thread. A thread is anchored to a line range, a file or a part of the explanation. A comment goes into the feedback. With Ask agent, the review agent also answers in the thread. Every thread is a conversation of its own, so three independent questions do not end up in one long chat.

It fits when you:

- let an agent work on its own and review the result afterwards
- review commits one by one after the agent made a series of them
- want to send the agent one list of fixes instead of a dozen chat messages

## Reviewing a change

1. Pick a scope in the top part of the sidebar. Uncommitted is the default.
2. On the Overview page, click Explain changes with Claude Code (or Codex). The answer streams in, and the sidebar regroups the files when it is done. Stop cancels the run. When the CLI is missing or fails, the overview shows its error output, and a failed question shows the error in its thread.
3. Open a group with Review group to read all its files one after the other, or open one file. Mark a file Viewed when you are done with it.
4. Comment: `+` in the gutter for one line, drag over the line numbers and use Comment on lines for a range, or Comment on file. The thread opens on the right. Comment saves your note. Ask agent (Cmd+Enter) also sends it to the review agent.
5. Open Feedback in the toolbar. Copy feedback copies the Markdown. Save to file... saves it, by default into the repository's git directory, and copies a prompt such as `Read the code review feedback in /repo/.git/agentdiff-feedback.md and address each item.` Paste either one into your coding agent.

Feedback for two comments:

````markdown
# Code review feedback

Scope: the uncommitted changes in the working tree: ...

Lines are 1-based. (+) marks lines of the new version, (-) lines of the old version. Address each item, or explain why not.

## cache/cache.go:6 (+)
```
	return strings.ToLower(key)
```
Keep keys case-sensitive, callers rely on it.

## api/handler.go (file-level)
Return a typed error here.
````

To see what an agent did, click Agent log in the toolbar, Log on the Overview page, or Log in a thread. Each run lists its steps in order: Command (a shell command you can paste into a terminal), Prompt, Session started (model and permission mode), Tool call and Tool result, Assistant, Result with turns, time and tokens, and the exit status. Copy puts the readable log on the clipboard. Show file opens the raw log in Finder.

Uncheck In feedback in a thread to leave it out, or mark it Resolved. The feedback page has the checkboxes Include agent replies and Include resolved threads.

## Configuration

Settings has two tabs. All reviews writes the global configuration. This review overrides it for the current repository and scope only.

| Field | Default | Purpose |
| --- | --- | --- |
| Agent | Claude Code | Which CLI writes the explanation and answers threads |
| Model | CLI default | Passed as `--model` (Claude) or `-m` (Codex) |
| Binary | found on the login shell's `PATH` | Full path to the CLI, `~/` allowed |
| Extra arguments | none | Added to the CLI call, split at spaces |
| Review questions | the six questions | The part of the explanation prompt you control |

The app wraps your questions in the scope, the diffstat, the list of changed files and the grouping request, because it parses the answer. A thread's session belongs to one agent. After you switch agents, the next question in an old thread goes to a new session of the new agent, with the thread's anchor and its earlier messages.

The app saves everything under `~/Library/Application Support/AgentDiff/`.

| File | Holds |
| --- | --- |
| `config.json` | global settings, recent repositories, split or unified diff |
| `reviews/<repo hash>/<scope>.json` | explanation, groups, threads, viewed files and overrides of one scope. The hash is of the repository path, so a moved repository starts with empty reviews |
| `reviews/<repo hash>/<scope>.runs/<run>.jsonl` | the raw log of one agent run: the run's details on the first line, then one line per command, prompt, output line, stderr line and exit. Each review keeps its last 50 runs. The logs hold the file contents the agent read |

A review outlives the changes it was written for, which matters most for the uncommitted scope. When the changed files no longer match the explanation's groups, the overview shows a warning. Start over deletes the review of the current scope and its run logs, after you confirm.

## Commands

| Command | What it does |
| --- | --- |
| `go tool mygo dev` | Builds a development app and restarts it on every change. It opens the `agentdiff` repository itself |
| `go tool mygo build` | Builds `AgentDiff.app` and a `.dmg` into `build/darwin-arm64/` |
| `go test ./...` | Runs all tests, with no network and no agent calls |
| `AGENTDIFF_SNAPSHOTS=/tmp/snaps go test .` | Also writes PNG screenshots of the main screens, rendered without a window, into an existing directory |
| `AGENTDIFF_E2E=claude go test -run TestEndToEnd -v .` | Asks a real agent (`claude` or `codex`) to explain this repository's last commit, then asks a question in a forked session. It uses your subscription. `AGENTDIFF_E2E_MODEL` sets the model |

## Project layout

```
main.go, app.go, run.go   window, app state, agent runs and streaming
view*.go                  one file per screen: overview, files, thread panel, feedback, settings
internal/gitx             git CLI wrapper: scopes, changed files, file contents, commits
internal/agent            claude and codex CLI runner: arguments, stream parsing, login-shell PATH
internal/review           review model, explanation prompt and parsing, feedback export
internal/store            JSON files for settings and reviews
```

Only the packages under `internal/` and the views talk to git and the CLIs. Views never block: git and agent work runs on goroutines and reaches the view through `Window.Update`.

## FAQ

**Why a native app and not a page in the browser?**
MyGo's native UI ships a diff viewer with gutter buttons and room for comments under lines. Its Markdown view is built for streamed replies. The app starts at once and does not need a server.

**Why does the agent explain the changes instead of the app reading the diff itself?**
The agent can read the files around the change, the commit messages and the tests. It also sees the reasons behind a change that a diff does not show, which is what the questions about non-pretty changes ask for.

**Can the review agent change my code?**
Codex cannot: it runs in its `read-only` sandbox. Claude refuses every Bash call that matches neither the app's allow list nor an allow rule in your own Claude settings. So Claude can only write when your settings allow a write command. Your coding agent applies the feedback, in its own session.

**When should I not use it?**
For a two-line change: `git diff` is faster. `agentdiff` does not post to GitHub either. Review pull requests of other people in GitHub.

**What does it not do yet?**
Comments stay on the line numbers they were written on, so after the agent edits a file they can point at other code. The quoted code in the feedback shows what was meant. The app does not watch files: click Refresh (Cmd+R) after the agent changes something. The app is not single-instance: each `open -n` starts another copy with its own window.

## License

MIT
