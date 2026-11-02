//go:build !windows

package agent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/psd-coder/agentdiff/internal/proc"
)

const (
	envMarker    = "__AGENTDIFF_ENV__"
	shellTimeout = 5 * time.Second
	pathSource   = "login shell PATH"
)

// loadLoginEnv reads the environment of the user's login shell. An app
// opened from the Dock or a desktop menu gets a short PATH without the
// folders the shell's rc files add.
func loadLoginEnv() []string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		}
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
	proc.Session(cmd)
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

func fallbackDirs() []string {
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append([]string{filepath.Join(home, ".local", "bin")}, dirs...)
	}
	return dirs
}

func executableNames(file string) []string { return []string{file} }

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
