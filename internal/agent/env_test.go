package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExtractEnv(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    []string
		wantErr bool
	}{
		{
			name: "junk before marker",
			out:  "Welcome!\nPATH=/fake\n" + envMarker + "\nPATH=/a:/b\x00HOME=/h\x00MULTI=x\ny\x00",
			want: []string{"PATH=/a:/b", "HOME=/h", "MULTI=x\ny"},
		},
		{name: "invalid entries dropped", out: envMarker + "\n=bad\x00noequals\x00PATH=/a\x00", want: []string{"PATH=/a"}},
		{name: "no marker", out: "PATH=/a\x00", wantErr: true},
		{name: "no PATH", out: envMarker + "\nHOME=/h\x00", wantErr: true},
		{name: "empty", out: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractEnv([]byte(tt.out))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("env = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShellEnv(t *testing.T) {
	shell := writeScript(t, `echo "rc noise"; eval "$3"`)
	env, err := shellEnv(shell, []string{"-l", "-c"})
	if err != nil {
		t.Fatal(err)
	}
	if lookupEnv(env, "PATH") != os.Getenv("PATH") {
		t.Errorf("PATH = %q", lookupEnv(env, "PATH"))
	}
}

func TestShellEnvFailure(t *testing.T) {
	shell := writeScript(t, "exit 1")
	if _, err := shellEnv(shell, []string{"-l", "-c"}); err == nil {
		t.Error("want error")
	}
}

func TestWithFallbackPath(t *testing.T) {
	env := withFallbackPath([]string{"HOME=/h", "PATH=/custom:/usr/bin"})
	path := lookupEnv(env, "PATH")
	dirs := filepath.SplitList(path)
	if dirs[0] != "/custom" || dirs[1] != "/usr/bin" {
		t.Errorf("PATH order changed: %q", path)
	}
	if strings.Count(path, "/usr/bin") != 1 || !strings.Contains(path, "/opt/homebrew/bin") {
		t.Errorf("PATH = %q", path)
	}
	if lookupEnv(env, "HOME") != "/h" || strings.Count(strings.Join(env, "\n"), "PATH=") != 1 {
		t.Errorf("env = %q", env)
	}
	if got := lookupEnv(withFallbackPath(nil), "PATH"); !strings.Contains(got, "/bin") {
		t.Errorf("PATH without env = %q", got)
	}
}

func TestLookPath(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(dir1, "claude"), 0o644)
	mustWrite(t, filepath.Join(dir2, "claude"), 0o755)
	if err := os.Mkdir(filepath.Join(dir1, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := strings.Join([]string{"", dir1, dir2}, string(filepath.ListSeparator))

	if got, err := lookPath("claude", path); err != nil || got != filepath.Join(dir2, "claude") {
		t.Errorf("lookPath claude = %q, %v", got, err)
	}
	if _, err := lookPath("codex", path); err == nil {
		t.Error("directory must not match")
	}
	if _, err := lookPath("claude", ""); err == nil {
		t.Error("empty PATH must not match")
	}
}

func TestResolveBinaryUsesEnvPath(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "codex"), 0o755)
	got, err := resolveBinary(Config{Provider: Codex}, []string{"PATH=" + dir})
	if err != nil || got != filepath.Join(dir, "codex") {
		t.Errorf("resolveBinary = %q, %v", got, err)
	}
}

func mustWrite(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}
