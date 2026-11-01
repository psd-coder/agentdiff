package agent

import "cmp"

// RunStatus is how a run ended. RunUnknown is a run that is still going,
// or whose log ends before the process exits.
type RunStatus int

const (
	RunUnknown RunStatus = iota
	RunDone
	RunFailed
	RunStopped
)

func exitStatus(text string) RunStatus {
	switch text {
	case exitOK:
		return RunDone
	case exitStopped:
		return RunStopped
	}
	return RunFailed
}

func failedStatus(failed bool) RunStatus {
	if failed {
		return RunFailed
	}
	return RunDone
}

// Status is how the run with these log items ended. Once the agent sent
// its result, the result decides: the CLI can still run its hooks after
// it, and older logs say "Stopped" when the app cancelled them then. An
// exit with status 0 before any result is a failure, because the agent
// gave no answer.
func Status(items []LogItem) RunStatus {
	result, exit := RunUnknown, RunUnknown
	for _, it := range items {
		result = cmp.Or(it.result, result)
		exit = cmp.Or(it.exit, exit)
	}
	switch {
	case result != RunUnknown:
		return result
	case exit == RunDone:
		return RunFailed
	}
	return exit
}

// SumUsage adds up what the log items of a run report as spent. It is nil
// when none of them reports it.
func SumUsage(items []LogItem) *Usage {
	var sum *Usage
	for _, it := range items {
		if it.Usage == nil {
			continue
		}
		if sum == nil {
			sum = &Usage{}
		}
		sum.Add(*it.Usage)
	}
	return sum
}

// OwnCost is what a run spent itself. Claude reports the cost of the whole
// session, so a run that resumed a session costs the difference to parent,
// the run that ended in that session. The cost stays unknown, 0, when the
// run resumed a session and parent is nil.
func OwnCost(usage Usage, resumed bool, parent *Usage) float64 {
	switch {
	case usage.SessionCost == 0 || !resumed:
		return cmp.Or(usage.SessionCost, usage.CostUSD)
	case parent == nil:
		return 0
	}
	return max(0, usage.SessionCost-parent.SessionCost)
}
