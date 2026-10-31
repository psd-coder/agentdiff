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
	t        *testing.T
	mu       sync.Mutex
	reply    fakeReply
	requests []agent.Request
	held     []chan struct{}
	running  sync.WaitGroup
}

type fakeReply struct {
	session string
	answer  string
	err     error
	// hold keeps the run going until release or until it is cancelled.
	hold bool
	// streamed and activity reach the app before the hold, as text the
	// agent writes and as what it does now.
	streamed string
	activity string
}

func newFakeAgent(t *testing.T, a *app) *fakeAgent {
	t.Helper()
	f := &fakeAgent{t: t}
	a.startAgent = f.run
	t.Cleanup(func() {
		f.release()
		f.running.Wait()
	})
	return f
}

// answer makes the next runs answer text in session, without a hold.
func (f *fakeAgent) answer(session, text string) {
	f.set(fakeReply{session: session, answer: text})
}

// fail makes the next runs fail with err before they start a session.
func (f *fakeAgent) fail(err error) { f.set(fakeReply{err: err}) }

// hold makes the next runs wait for release, then answer as before.
func (f *fakeAgent) hold() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply.hold = true
}

func (f *fakeAgent) set(r fakeReply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply = r
}

// release lets the held runs go on.
func (f *fakeAgent) release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.held {
		close(c)
	}
	f.held = nil
}

// received is every request the runs got, oldest first.
func (f *fakeAgent) received() []agent.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.Request(nil), f.requests...)
}

func (f *fakeAgent) last() agent.Request {
	f.t.Helper()
	reqs := f.received()
	if len(reqs) == 0 {
		f.t.Fatal("the agent got no request")
	}
	return reqs[len(reqs)-1]
}

func (f *fakeAgent) run(ctx context.Context, cfg agent.Config, req agent.Request) <-chan agent.Event {
	sink := req.Log
	req.Log = nil
	f.mu.Lock()
	r := f.reply
	f.requests = append(f.requests, req)
	var release chan struct{}
	if r.hold {
		release = make(chan struct{})
		f.held = append(f.held, release)
	}
	f.running.Add(1)
	f.mu.Unlock()
	out := output(cfg.Provider, r)

	ch := make(chan agent.Event, 4)
	go func() {
		defer f.running.Done()
		defer close(ch)
		log := func(kind agent.LogKind, text string) {
			if sink != nil {
				sink(agent.LogEntry{Time: time.Now(), Kind: kind, Text: text})
			}
		}
		if r.err != nil {
			log(agent.LogExit, "Could not start: "+r.err.Error())
			ch <- agent.Event{Kind: agent.EventError, Err: r.err}
			return
		}
		log(agent.LogCommand, string(cfg.Provider))
		log(agent.LogStdin, req.Prompt)
		if r.streamed != "" {
			ch <- agent.Event{Kind: agent.EventText, Text: r.streamed}
		}
		if r.activity != "" {
			ch <- agent.Event{Kind: agent.EventActivity, Text: r.activity}
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
		ch <- agent.Event{Kind: agent.EventSession, SessionID: r.session}
		for _, line := range out[1:] {
			log(agent.LogStdout, line)
		}
		ch <- agent.Event{Kind: agent.EventDone, Text: r.answer}
		log(agent.LogExit, "Exited with status 0")
	}()
	return ch
}

// output is what the CLI of p prints for r, one JSON line each: the
// session first, then a tool call, the answer and the tokens it used.
func output(p agent.Provider, r fakeReply) []string {
	if p == agent.Codex {
		return []string{
			mustJSON(map[string]any{"type": "thread.started", "thread_id": r.session}),
			mustJSON(map[string]any{"type": "item.completed", "item": map[string]string{"id": "item_0", "type": "agent_message", "text": r.answer}}),
			`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}`,
		}
	}
	return []string{
		mustJSON(map[string]any{"type": "system", "subtype": "init", "session_id": r.session}),
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}}],"usage":{"input_tokens":5,"cache_read_input_tokens":20000,"cache_creation_input_tokens":3000,"output_tokens":995}}}`,
		mustJSON(map[string]any{
			"type": "result", "subtype": "success", "result": r.answer, "session_id": r.session, "total_cost_usd": 0.25,
			"usage":      map[string]int{"input_tokens": 10, "output_tokens": 1000, "cache_read_input_tokens": 20000, "cache_creation_input_tokens": 5000},
			"modelUsage": map[string]any{"m": map[string]int{"inputTokens": 10, "outputTokens": 1000, "cacheReadInputTokens": 20000, "cacheCreationInputTokens": 5000}},
		}),
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
