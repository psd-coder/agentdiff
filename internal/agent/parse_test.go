package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func parseFixture(t *testing.T, parser lineParser, name string) []Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(nil, maxLineSize)
	var events []Event
	for scanner.Scan() {
		events = append(events, parser.parse(scanner.Bytes())...)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func parseLines(parser lineParser, lines ...string) []Event {
	var events []Event
	for _, l := range lines {
		events = append(events, parser.parse([]byte(l))...)
	}
	return events
}

func kinds(events []Event) []EventKind {
	out := make([]EventKind, len(events))
	for i, ev := range events {
		out[i] = ev.Kind
	}
	return out
}

func joinText(events []Event, kind EventKind) string {
	var builder strings.Builder
	for _, ev := range events {
		if ev.Kind == kind {
			builder.WriteString(ev.Text)
		}
	}
	return builder.String()
}

func activities(events []Event) []string {
	var out []string
	for _, ev := range events {
		if ev.Kind == EventActivity {
			out = append(out, ev.Text)
		}
	}
	return out
}

func TestClaudeFixtures(t *testing.T) {
	tests := []struct {
		fixture    string
		session    string
		text       string
		activities []string
		kinds      []EventKind
	}{
		{
			fixture: "claude_stream.jsonl",
			session: "9f9041fb-0b2b-45dc-aee9-9dd0c543902f",
			text:    "hi there",
			kinds:   []EventKind{EventSession, EventText, EventDone},
		},
		{
			fixture:    "claude_tool.jsonl",
			session:    "6a699d1b-afa7-40a4-b527-0cf3cd8745ca",
			text:       "Scaffold MyGo native app",
			activities: []string{"Bash: git -C /Users/psdcoder/projects/pet/agentdiff log -1 --oneline"},
			kinds:      []EventKind{EventSession, EventActivity, EventText, EventText, EventDone},
		},
		{
			fixture: "claude_fork.jsonl",
			session: "4d856974-081d-4842-b5be-2c804b37991b",
			text:    `You asked me to reply with exactly "hi there".`,
			kinds:   []EventKind{EventSession, EventText, EventText, EventDone},
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			events := parseFixture(t, &claudeParser{}, tt.fixture)
			if got := kinds(events); !reflect.DeepEqual(got, tt.kinds) {
				t.Fatalf("kinds = %v, want %v", got, tt.kinds)
			}
			if events[0].SessionID != tt.session {
				t.Errorf("session = %q, want %q", events[0].SessionID, tt.session)
			}
			if got := joinText(events, EventText); got != tt.text {
				t.Errorf("text = %q, want %q", got, tt.text)
			}
			done := events[len(events)-1]
			if done.Text != tt.text || done.SessionID != tt.session {
				t.Errorf("done = %+v", done)
			}
			if got := activities(events); !reflect.DeepEqual(got, tt.activities) {
				t.Errorf("activities = %q, want %q", got, tt.activities)
			}
		})
	}
}

func TestClaudeParserEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		dir   string
		lines []string
		want  []Event
	}{
		{
			name:  "empty and malformed lines",
			lines: []string{"", "not json", `{"type":`, `{"type":"unknown_thing"}`},
		},
		{
			name: "session emitted once",
			lines: []string{
				`{"type":"system","session_id":"a"}`,
				`{"type":"system","session_id":"b"}`,
			},
			want: []Event{{Kind: EventSession, SessionID: "a"}},
		},
		{
			name: "assistant text without deltas",
			lines: []string{
				`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"one"}]}}`,
				`{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"two"}]}}`,
			},
			want: []Event{{Kind: EventText, Text: "one"}, {Kind: EventText, Text: "\n\ntwo"}},
		},
		{
			name: "deltas without api_message_id use message_start id",
			lines: []string{
				`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1"}}}`,
				`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}`,
				`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}}`,
				`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"hi"}]}}`,
			},
			want: []Event{{Kind: EventText, Text: "hi"}},
		},
		{
			name: "text blocks of separate messages are separated",
			lines: []string{
				`{"type":"stream_event","api_message_id":"m1","event":{"type":"content_block_start","content_block":{"type":"text"}}}`,
				`{"type":"stream_event","api_message_id":"m1","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Checking."}}}`,
				`{"type":"stream_event","api_message_id":"m2","event":{"type":"content_block_start","content_block":{"type":"text"}}}`,
				`{"type":"stream_event","api_message_id":"m2","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Done"}}}`,
			},
			want: []Event{{Kind: EventText, Text: "Checking."}, {Kind: EventText, Text: "\n\nDone"}},
		},
		{
			name: "input_json_delta and empty text_delta ignored",
			lines: []string{
				`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"a"}}}`,
				`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}}`,
			},
		},
		{
			name: "subagent text skipped, its tool calls kept",
			lines: []string{
				`{"type":"stream_event","parent_tool_use_id":"t1","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"sub"}}}`,
				`{"type":"assistant","parent_tool_use_id":"t1","message":{"id":"s1","content":[{"type":"text","text":"sub"},{"type":"tool_use","name":"Glob","input":{"pattern":"**/*.go"}}]}}`,
			},
			want: []Event{{Kind: EventActivity, Text: "Glob: **/*.go"}},
		},
		{
			name: "tool activity lines",
			dir:  "/repo",
			lines: []string{
				`{"type":"assistant","message":{"id":"m1","content":[` +
					`{"type":"tool_use","name":"Read","input":{"file_path":"/repo/internal/x.go"}},` +
					`{"type":"tool_use","name":"Read","input":{"file_path":"/other/y.go"}},` +
					`{"type":"tool_use","name":"Grep","input":{"pattern":"func Run","path":"/repo/internal"}},` +
					`{"type":"tool_use","name":"Bash","input":{"command":"git diff \\\n  --stat"}},` +
					`{"type":"tool_use","name":"TodoWrite","input":{"todos":[]}}]}}`,
			},
			want: []Event{
				{Kind: EventActivity, Text: "Read: internal/x.go"},
				{Kind: EventActivity, Text: "Read: /other/y.go"},
				{Kind: EventActivity, Text: "Grep: func Run in internal"},
				{Kind: EventActivity, Text: `Bash: git diff \ --stat`},
				{Kind: EventActivity, Text: "TodoWrite"},
			},
		},
		{
			name:  "error result",
			lines: []string{`{"type":"result","subtype":"success","is_error":true,"result":"API Error: 529"}`},
			want:  []Event{{Kind: EventError}},
		},
		{
			name:  "error subtype without result",
			lines: []string{`{"type":"result","subtype":"error_max_turns","is_error":false}`},
			want:  []Event{{Kind: EventError}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLines(&claudeParser{dir: tt.dir}, tt.lines...)
			assertEvents(t, got, tt.want)
		})
	}
}

func TestClaudeErrorMessage(t *testing.T) {
	tests := map[string]string{
		`{"type":"result","is_error":true,"result":"API Error: 529"}`: "API Error: 529",
		`{"type":"result","subtype":"error_max_turns"}`:               "error_max_turns",
	}
	for line, want := range tests {
		events := parseLines(&claudeParser{}, line)
		if len(events) != 1 || events[0].Err == nil || events[0].Err.Error() != want {
			t.Errorf("%s: got %+v, want error %q", line, events, want)
		}
	}
}

func TestCodexFixtures(t *testing.T) {
	t.Run("exec", func(t *testing.T) {
		events := parseFixture(t, &codexParser{}, "codex_exec.jsonl")
		assertEvents(t, events, []Event{
			{Kind: EventSession, SessionID: "01a1735a-bf7c-7a33-8851-e43fd5cba799"},
			{Kind: EventText, Text: "hi there"},
			{Kind: EventDone, Text: "hi there", SessionID: "01a1735a-bf7c-7a33-8851-e43fd5cba799"},
		})
	})
	t.Run("fork", func(t *testing.T) {
		const session = "01a1735b-1b54-7643-afe2-fd2db860c144"
		first := "I’ll check the latest commit subject."
		second := "Latest subject: Scaffold MyGo native app\n\nYou first asked: “Reply with exactly: hi there”"
		events := parseFixture(t, &codexParser{}, "codex_fork.jsonl")
		assertEvents(t, events, []Event{
			{Kind: EventSession, SessionID: session},
			{Kind: EventText, Text: first},
			{Kind: EventActivity, Text: "Running: rtk git log -1 --oneline"},
			{Kind: EventText, Text: "\n\n" + second},
			{Kind: EventDone, Text: first + "\n\n" + second, SessionID: session},
		})
	})
}

func TestCodexParserEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		lines     []string
		want      []Event
		streamErr string
	}{
		{
			name:  "empty and malformed lines",
			lines: []string{"", "{", "[]", `{"type":"item.completed"}`, `{"type":"reasoning"}`},
		},
		{
			name: "completed command without started is reported",
			lines: []string{
				`{"type":"item.completed","item":{"id":"i1","type":"command_execution","command":"bash -lc \"echo \\\"hi\\\"\""}}`,
			},
			want: []Event{{Kind: EventActivity, Text: `Running: echo "hi"`}},
		},
		{
			name: "empty agent message skipped",
			lines: []string{
				`{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":""}}`,
				`{"type":"turn.completed"}`,
			},
			want: []Event{{Kind: EventDone}},
		},
		{
			name:      "error event is not terminal",
			lines:     []string{`{"type":"error","message":"Reconnecting... 1/5"}`},
			want:      []Event{{Kind: EventActivity, Text: "Reconnecting... 1/5"}},
			streamErr: "Reconnecting... 1/5",
		},
		{
			name:  "turn failed",
			lines: []string{`{"type":"turn.failed","error":{"message":"quota exceeded"}}`},
			want:  []Event{{Kind: EventError}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &codexParser{}
			assertEvents(t, parseLines(parser, tt.lines...), tt.want)
			if got := errString(parser.streamError()); got != tt.streamErr {
				t.Errorf("streamError = %q, want %q", got, tt.streamErr)
			}
		})
	}
}

func TestCodexTurnFailedMessage(t *testing.T) {
	tests := []struct {
		lines []string
		want  string
	}{
		{[]string{`{"type":"turn.failed","error":{"message":"quota exceeded"}}`}, "quota exceeded"},
		{[]string{`{"type":"error","message":"stream closed"}`, `{"type":"turn.failed"}`}, "stream closed"},
		{[]string{`{"type":"turn.failed","error":{}}`}, "codex turn failed"},
	}
	for _, tt := range tests {
		events := parseLines(&codexParser{}, tt.lines...)
		last := events[len(events)-1]
		if last.Kind != EventError || errString(last.Err) != tt.want {
			t.Errorf("%v: got %+v, want error %q", tt.lines, last, tt.want)
		}
	}
}

func TestCommandText(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{`"/bin/zsh -lc 'git diff --stat'"`, "git diff --stat"},
		{`"/bin/bash -c 'echo '\\''hi'\\'''"`, "echo 'hi'"},
		{`"bash -lc \"grep \\\"a b\\\" x\""`, `grep "a b" x`},
		{`"sh -c ls"`, "ls"},
		{`"git status"`, "git status"},
		{`"/bin/zsh -lc"`, "/bin/zsh -lc"},
		{`["/bin/zsh","-lc","git log -1"]`, "git log -1"},
		{`["git","log","-1"]`, "git log -1"},
		{`null`, ""},
		{`42`, ""},
	}
	for _, tt := range tests {
		if got := commandText([]byte(tt.raw)); got != tt.want {
			t.Errorf("commandText(%s) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func assertEvents(t *testing.T, got, want []Event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d events %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		gotEvent, wantEvent := got[i], want[i]
		if gotEvent.Kind != wantEvent.Kind || gotEvent.Text != wantEvent.Text || gotEvent.SessionID != wantEvent.SessionID {
			t.Errorf("event %d = %+v, want %+v", i, gotEvent, wantEvent)
		}
		if (gotEvent.Kind == EventError) != (gotEvent.Err != nil) {
			t.Errorf("event %d: kind %v with err %v", i, gotEvent.Kind, gotEvent.Err)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestOpenCodeConfig(t *testing.T) {
	var config struct {
		Share string `json:"share"`
		Agent map[string]struct {
			Prompt     string          `json:"prompt"`
			Permission json.RawMessage `json:"permission"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(opencodeConfig("be brief")), &config); err != nil {
		t.Fatal(err)
	}
	agent := config.Agent["agentdiff"]
	if config.Share != "disabled" || agent.Prompt != "be brief" {
		t.Errorf("config = %+v", config)
	}
	permission := string(agent.Permission)
	for _, rules := range []string{
		`{"*":"deny","read":"allow",`,
		`"external_directory":"deny","bash":{"*":"deny","git diff":"allow","git diff *":"allow",`,
		`"wc *":"allow","*\u003e*":"deny","*--output*":"deny"}}`,
	} {
		if !strings.Contains(permission, rules) {
			t.Errorf("permission does not hold %s in order:\n%s", rules, permission)
		}
	}
	var withoutPrompt struct {
		Agent map[string]map[string]any `json:"agent"`
	}
	if err := json.Unmarshal([]byte(opencodeConfig("")), &withoutPrompt); err != nil {
		t.Fatal(err)
	}
	if _, ok := withoutPrompt.Agent["agentdiff"]["prompt"]; ok {
		t.Error("an empty system prompt replaces the prompt of OpenCode")
	}
}

func TestOpenCodeEnvKeepsUserSetup(t *testing.T) {
	isolated := strings.Join(opencodeEnv(Config{}, Request{}), "\n")
	own := strings.Join(opencodeEnv(Config{UserSetup: true}, Request{}), "\n")
	for _, name := range []string{"OPENCODE_CONFIG_DIR=", "OPENCODE_DISABLE_PROJECT_CONFIG=1", "OPENCODE_DISABLE_CLAUDE_CODE=1", "OPENCODE_DISABLE_EXTERNAL_SKILLS=1"} {
		if !strings.Contains(isolated, name) || strings.Contains(own, name) {
			t.Errorf("%s: without the user's setup %v, with it %v", name, strings.Contains(isolated, name), strings.Contains(own, name))
		}
	}
	if !strings.HasPrefix(own, "OPENCODE_CONFIG_CONTENT=") {
		t.Errorf("with the user's setup = %q", own)
	}
}

func TestOpenCodeFixtures(t *testing.T) {
	t.Run("tools", func(t *testing.T) {
		const session = "ses_e5e2a9c03ffewCdJfh3pImwkOs"
		events := parseFixture(t, &opencodeParser{dir: "/Users/me/repo"}, "opencode_tools.jsonl")
		assertEvents(t, events, []Event{
			{Kind: EventSession, SessionID: session},
			{Kind: EventActivity, Text: "Read: README.md"},
			{Kind: EventActivity, Text: "Bash: git log -1 > /tmp/x_oc_test"},
			{Kind: EventActivity, Text: "Bash: git log -1 --oneline"},
			{Kind: EventText, Text: "DONE-TEXT"},
			{Kind: EventDone, Text: "DONE-TEXT", SessionID: session},
		})
	})
	t.Run("error", func(t *testing.T) {
		parser := &opencodeParser{}
		assertEvents(t, parseFixture(t, parser, "opencode_error.jsonl"), []Event{{Kind: EventSession, SessionID: "ses_e60e60795ffetobe42K4hQutc0"}})
		if got := errString(parser.streamError()); got != "invalid x-api-key" {
			t.Errorf("streamError = %q", got)
		}
	})
}

func TestOpenCodeParserEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		lines     []string
		want      []Event
		streamErr string
	}{
		{
			name:  "empty and malformed lines",
			lines: []string{"", "{", "[]", `{"type":"text"}`, `{"type":"tool_use"}`, `{"type":"step_finish"}`},
		},
		{
			name: "texts of several steps join into the answer",
			lines: []string{
				`{"type":"text","part":{"text":"Let me look."}}`,
				`{"type":"step_finish","part":{"reason":"tool-calls"}}`,
				`{"type":"text","part":{"text":""}}`,
				`{"type":"text","part":{"text":"Done."}}`,
				`{"type":"step_finish","part":{"reason":"length"}}`,
			},
			want: []Event{
				{Kind: EventText, Text: "Let me look."},
				{Kind: EventText, Text: "\n\nDone."},
				{Kind: EventDone, Text: "Let me look.\n\nDone."},
			},
		},
		{
			name:      "error without a message gives its name",
			lines:     []string{`{"type":"error","error":{"name":"ProviderAuthError","data":{}}}`, `{"type":"error"}`},
			streamErr: "ProviderAuthError\nopencode reported an error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &opencodeParser{}
			assertEvents(t, parseLines(parser, tt.lines...), tt.want)
			if got := errString(parser.streamError()); got != tt.streamErr {
				t.Errorf("streamError = %q, want %q", got, tt.streamErr)
			}
		})
	}
}
