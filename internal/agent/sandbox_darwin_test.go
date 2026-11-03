package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenCodeCannotWriteToRepository(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	bin := writeScript(t, "echo x > inside.txt\necho x > "+outside+"\n/bin/cat "+fixturePath(t, "opencode_tools.jsonl"))
	last := assertOneTerminal(t, collect(t, Run(t.Context(), Config{Provider: OpenCode, Binary: bin}, Request{Dir: dir}), 5*time.Second))
	if last.Kind != EventDone {
		t.Fatalf("last = %+v", last)
	}
	if _, err := os.Stat(filepath.Join(dir, "inside.txt")); !os.IsNotExist(err) {
		t.Errorf("the run wrote into the repository: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the run could not write outside the repository: %v", err)
	}
}
