// Package proc starts child processes so that a cancel stops them with
// their children, and so that they open no console window on Windows.
package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW of the Windows API.
const createNoWindow = 0x08000000

// Hide keeps cmd from opening a console window. The app has no console,
// so Windows would open one for each console program it starts.
func Hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}

// Group starts cmd without a console window, and has the cancel of its
// context kill it with its children.
func Group(cmd *exec.Cmd) {
	Hide(cmd)
	cmd.Cancel = func() error { return killTree(cmd) }
}

// killTree kills the process with its children, which Windows keeps
// running when their parent ends.
func killTree(cmd *exec.Cmd) error {
	taskkill := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
	kill := exec.Command(taskkill, "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	Hide(kill)
	if kill.Run() != nil {
		return cmd.Process.Kill()
	}
	return nil
}
