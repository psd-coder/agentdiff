//go:build !windows

// Package proc starts child processes so that a cancel stops them with
// their children, and so that they open no console window on Windows.
package proc

import (
	"os/exec"
	"syscall"
	"time"
)

// Hide keeps cmd from opening a console window. Only Windows opens one.
func Hide(*exec.Cmd) {}

// Group starts cmd in a process group of its own, which the cancel of its
// context kills whole.
func Group(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
}

// Session starts cmd in a session of its own, without a controlling
// terminal, which the cancel of its context kills whole. Otherwise an
// interactive shell started from a terminal in a background group gets
// stopped by SIGTTIN or SIGTTOU.
func Session(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
}

// killGroup kills the process and its children. A child forked while the
// signal is delivered can miss it on macOS and keep stdout open, so the
// kill repeats until the group is gone.
func killGroup(cmd *exec.Cmd) error {
	pgid := -cmd.Process.Pid
	err := syscall.Kill(pgid, syscall.SIGKILL)
	go func() {
		for range 20 {
			time.Sleep(50 * time.Millisecond)
			if syscall.Kill(pgid, syscall.SIGKILL) != nil {
				return
			}
		}
	}()
	return err
}
