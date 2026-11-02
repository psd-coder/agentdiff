//go:build !windows

package gitx

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"
)

func TestContentReaderCancelledMidRead(t *testing.T) {
	repo := uncommittedFixture(t).open()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	cr := snapshot(t, repo, Scope{Kind: ScopeUncommitted}).NewContentReader(ctx)
	pid := cr.cmd.Process.Pid
	t.Cleanup(func() {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Error(err)
		}
	})
	// A stopped git reads the request and never answers, so Contents
	// waits in the read until the context ends.
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := cr.Contents(FileChange{Path: "mod.txt", Status: StatusModified})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Contents() error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Contents() still waits after the context ended")
	}
	if err := cr.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if cr.cmd.ProcessState == nil {
		t.Error("git is still running after Close")
	}
}
