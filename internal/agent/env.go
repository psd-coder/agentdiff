package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	envMarker    = "__AGENTDIFF_ENV__"
	shellTimeout = 5 * time.Second
)

var loginEnv = sync.OnceValue(loadLoginEnv)

func loadLoginEnv() []string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	for _, flags := range [][]string{{"-l", "-i", "-c"}, {"-l", "-c"}} {
		if env, err := shellEnv(shell, flags); err == nil {
			return withFallbackPath(env)
		}
	}
	return withFallbackPath(os.Environ())
}

func shellEnv(shell string, flags []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()
	// rc files of an interactive shell may print to stdout, so the marker shows where env starts.
	script := "echo " + envMarker + "; exec env -0"
	cmd := exec.CommandContext(ctx, shell, append(flags, script)...)
	// A new session has no controlling tty. Otherwise an interactive shell started
	// from a terminal in a background group gets stopped by SIGTTIN/SIGTTOU.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return extractEnv(out)
}

func extractEnv(out []byte) ([]string, error) {
	start := bytes.Index(out, []byte(envMarker+"\n"))
	if start < 0 {
		return nil, errors.New("env marker not found")
	}
	env := parseEnv(out[start+len(envMarker)+1:])
	if lookupEnv(env, "PATH") == "" {
		return nil, errors.New("PATH not found in shell env")
	}
	return env, nil
}

func parseEnv(data []byte) []string {
	var env []string
	for entry := range strings.SplitSeq(string(data), "\x00") {
		if key, _, ok := strings.Cut(entry, "="); ok && key != "" {
			env = append(env, entry)
		}
	}
	return env
}

func lookupEnv(env []string, key string) string {
	for _, kv := range slices.Backward(env) {
		if value, ok := strings.CutPrefix(kv, key+"="); ok {
			return value
		}
	}
	return ""
}

func withFallbackPath(env []string) []string {
	dirs := filepath.SplitList(lookupEnv(env, "PATH"))
	for _, dir := range fallbackDirs() {
		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+strings.Join(dirs, string(filepath.ListSeparator)))
}

func fallbackDirs() []string {
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append([]string{filepath.Join(home, ".local", "bin")}, dirs...)
	}
	return dirs
}

func resolveBinary(cfg Config, env []string) (string, error) {
	if _, ok := lookup(cfg.Provider); !ok {
		return "", fmt.Errorf("unknown provider %q", cfg.Provider)
	}
	name := cfg.Binary
	if name == "" {
		name = string(cfg.Provider)
	}
	if rest, ok := strings.CutPrefix(name, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			name = filepath.Join(home, rest)
		}
	}
	if strings.Contains(name, "/") {
		if !isExecutable(name) {
			return "", fmt.Errorf("%s is not an executable file", name)
		}
		return name, nil
	}
	return lookPath(name, lookupEnv(env, "PATH"))
}

func lookPath(name, path string) (string, error) {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if isExecutable(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s not found in login shell PATH", name)
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
