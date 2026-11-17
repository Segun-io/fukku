package procgroup

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// After the last kill (made before the leader is reaped), a late kill from the size cap
// or the deadline signals nothing, so it cannot reach a process that reused the group id.
func TestLateKillDoesNothing(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Skip("sleep: ", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	g := &Group{cmd: cmd, done: true} // as after Last()
	g.Kill()
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("a kill after the last one reached the process: %v", err)
	}
	g = &Group{cmd: cmd}
	g.Last()
	if err := cmd.Wait(); err == nil {
		t.Fatal("the last kill did not reach the process")
	}
	g.Kill() // no process to signal, and no panic
}

// A cancelled context kills the whole group, and Run returns.
func TestRunCancelKillsTheGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sleep", "30")
	g := New(cmd)
	if !cmd.SysProcAttr.Setpgid {
		t.Fatal("New did not set Setpgid")
	}
	start := time.Now()
	if err := g.Run(); err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("a cancelled run: %v after %v", err, time.Since(start))
	}
	g.Kill() // after the last kill: nothing to signal, and no panic
}
