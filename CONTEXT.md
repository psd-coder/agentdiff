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
