# AgentDiff

AgentDiff asks a coding agent to explain a git diff, and collects the reviewer's comments on it into feedback for the agent.

## Language

**Run**:
One call of the agent that writes into one run target of a review. A review has at most one run per run target at a time.
_Avoid_: job, task, request

**Run target**:
What a run writes into: the overview, a walkthrough, or a thread. Each run target has an ID.
_Avoid_: key, destination

**Write-ahead**:
The deeper walkthroughs that the app queues and writes before the reviewer asks for them, so that Go deeper opens a written page. Only a few run at once over all reviews, and the others wait in order.
_Avoid_: prefetch, auto walk, background walk

**Runner**:
The part of the app that holds every run and the write-ahead queue. It decides when a run starts, which run a new one replaces, and how a run ends.
_Avoid_: scheduler, manager

**Outcome**:
How the latest answer of a run target ended: answered, failed, stopped, or none yet. A walkthrough counts its latest follow-up, and a thread its latest answer.
_Avoid_: result, status

**Stopped**:
The outcome of a run that the reviewer stopped. A run is **failed** when the agent or the CLI ended it with an error.
_Avoid_: cancelled, aborted

**Target state**:
What the app shows for a run target: queued, running, or its outcome. A run on the target goes before the outcome of the earlier run.
_Avoid_: status, run state

**Unseen**:
A run target whose run ended while the reviewer was not looking at it, until the reviewer opens it. A write-ahead walkthrough and a stopped run do not make a target unseen.
_Avoid_: unread, new

**Scope loader**:
The part of the app that reads the changes of a scope and the files the first page shows, and shows them at once. Only the latest load shows anything: a new load or a closed project drops the loads before it.
_Avoid_: loader state, load manager

**Load handle**:
What one load of a scope learns about the reviews: the saved review that has the same changes, the earlier reviews, and the commits that now hold their changes. It takes its steps in a fixed order.
_Avoid_: found, search

**Freshness**:
Whether the overview was written for the changes the scope has now: fresh, stale, or unknown while the changes are not read or an agent writes the overview.
_Avoid_: staleness check, drift state

**Keymap**:
The shortcut of each command: its default, or the one the reviewer gave it in the settings. No two commands share a shortcut.
_Avoid_: bindings, key config

**Reserved keys**:
Shortcuts that no command can take: those of the standard items of the menu bar, as Copy, and the keys the app uses in fields and lists.
_Avoid_: system keys, fixed keys

**Prompt template**:
The text of a prompt with blocks in it: the default text, the text in the settings, or a prompt file of the repository. The app sends it with the blocks filled in.
_Avoid_: prompt preset, prompt config

**Block**:
A part of a prompt template that the app writes, as `{{diffstat}}`. A template must keep its required blocks.
_Avoid_: placeholder, variable

**Output contract**:
The JSON that the app reads from an answer, which the `{{format}}` block asks for: the summary, the steps, the sections and, in the overview, the groups.
_Avoid_: schema, answer format

**Section**:
A part of a walkthrough next to its steps, which the prompt asks for by name, as the checklist. Its items get threads, and the reviewer cannot go deeper into them.
_Avoid_: part, extra block
