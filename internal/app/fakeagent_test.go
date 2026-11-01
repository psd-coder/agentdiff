package app

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
)

// fakeAgent starts the agent runs of a test app in process. Every run
// gets the reply set last. It logs the output that the CLI of its
// provider prints for that reply. The run log and the usage read that
// output as they read the output of the CLI.
type fakeAgent struct {
	ownerTest *testing.T
	mu        sync.Mutex
	reply     fakeReply
	requests  []agent.Request
	held      []chan struct{}
	running   sync.WaitGroup
}

type fakeReply struct {
	session string
	answer  string
	err     error
	// hold keeps the run going until release or until it is cancelled. A
	// run that fails waits for release only.
	hold bool
	// streamed and activity reach the app before the hold, as text the
	// agent writes and as what it does now.
	streamed string
	activity string
}

func newFakeAgent(t *testing.T, app *app) *fakeAgent {
	t.Helper()
	fakeAgent := &fakeAgent{ownerTest: t}
	app.startAgent = fakeAgent.run
	t.Cleanup(func() {
		fakeAgent.release()
		fakeAgent.running.Wait()
	})
	return fakeAgent
}

// answer makes the next runs answer text in session, without a hold.
func (fakeAgent *fakeAgent) answer(session, text string) {
	fakeAgent.set(fakeReply{session: session, answer: text})
}

// fail makes the next runs fail with err before they start a session.
func (fakeAgent *fakeAgent) fail(err error) { fakeAgent.set(fakeReply{err: err}) }

// hold makes the next runs wait for release, then answer as before.
func (fakeAgent *fakeAgent) hold() {
	fakeAgent.mu.Lock()
	defer fakeAgent.mu.Unlock()
	fakeAgent.reply.hold = true
}

func (fakeAgent *fakeAgent) set(fakeReply fakeReply) {
	fakeAgent.mu.Lock()
	defer fakeAgent.mu.Unlock()
	fakeAgent.reply = fakeReply
}

// release lets the held runs go on.
func (fakeAgent *fakeAgent) release() {
	fakeAgent.mu.Lock()
	defer fakeAgent.mu.Unlock()
	for _, c := range fakeAgent.held {
		close(c)
	}
	fakeAgent.held = nil
}

// received is every request the runs got, oldest first.
func (fakeAgent *fakeAgent) received() []agent.Request {
	fakeAgent.mu.Lock()
	defer fakeAgent.mu.Unlock()
	return append([]agent.Request(nil), fakeAgent.requests...)
}

func (fakeAgent *fakeAgent) last() agent.Request {
	fakeAgent.ownerTest.Helper()
	reqs := fakeAgent.received()
	if len(reqs) == 0 {
		fakeAgent.ownerTest.Fatal("the agent got no request")
	}
	return reqs[len(reqs)-1]
}

func (fakeAgent *fakeAgent) run(ctx context.Context, cfg agent.Config, req agent.Request) <-chan agent.Event {
	sink := req.Log
	req.Log = nil
	fakeAgent.mu.Lock()
	reply := fakeAgent.reply
	fakeAgent.requests = append(fakeAgent.requests, req)
	var release chan struct{}
	if reply.hold {
		release = make(chan struct{})
		fakeAgent.held = append(fakeAgent.held, release)
	}
	fakeAgent.running.Add(1)
	fakeAgent.mu.Unlock()
	out := output(cfg.Provider, reply)

	ch := make(chan agent.Event, 4)
	go func() {
		defer fakeAgent.running.Done()
		defer close(ch)
		log := func(kind agent.LogKind, text string) {
			if sink != nil {
				sink(agent.LogEntry{Time: time.Now(), Kind: kind, Text: text})
			}
		}
		if reply.err != nil {
			if release != nil {
				<-release
			}
			log(agent.LogExit, "Could not start: "+reply.err.Error())
			ch <- agent.Event{Kind: agent.EventError, Err: reply.err}
			return
		}
		log(agent.LogCommand, string(cfg.Provider))
		log(agent.LogStdin, req.Prompt)
		if reply.streamed != "" {
			ch <- agent.Event{Kind: agent.EventText, Text: reply.streamed}
		}
		if reply.activity != "" {
			ch <- agent.Event{Kind: agent.EventActivity, Text: reply.activity}
		}
		if release != nil {
			select {
			case <-release:
			case <-ctx.Done():
				log(agent.LogExit, "Stopped")
				ch <- agent.Event{Kind: agent.EventError, Err: ctx.Err()}
				return
			}
		}
		log(agent.LogStdout, out[0])
		ch <- agent.Event{Kind: agent.EventSession, SessionID: reply.session}
		for _, line := range out[1:] {
			log(agent.LogStdout, line)
		}
		ch <- agent.Event{Kind: agent.EventDone, Text: reply.answer}
		log(agent.LogExit, "Exited with status 0")
	}()
	return ch
}

// output is what the CLI of p prints for r, one JSON line each: the
// session first, then a tool call, the answer and the tokens it used.
func output(provider agent.Provider, fakeReply fakeReply) []string {
	if provider == agent.Codex {
		return []string{
			mustJSON(map[string]any{"type": "thread.started", "thread_id": fakeReply.session}),
			mustJSON(map[string]any{"type": "item.completed", "item": map[string]string{"id": "item_0", "type": "agent_message", "text": fakeReply.answer}}),
			`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}`,
		}
	}
	return []string{
		mustJSON(map[string]any{"type": "system", "subtype": "init", "session_id": fakeReply.session}),
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}}],"usage":{"input_tokens":5,"cache_read_input_tokens":20000,"cache_creation_input_tokens":3000,"output_tokens":995}}}`,
		mustJSON(map[string]any{
			"type": "result", "subtype": "success", "result": fakeReply.answer, "session_id": fakeReply.session, "total_cost_usd": 0.25,
			"usage":      map[string]int{"input_tokens": 10, "output_tokens": 1000, "cache_read_input_tokens": 20000, "cache_creation_input_tokens": 5000},
			"modelUsage": map[string]any{"m": map[string]int{"inputTokens": 10, "outputTokens": 1000, "cacheReadInputTokens": 20000, "cacheCreationInputTokens": 5000}},
		}),
	}
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
