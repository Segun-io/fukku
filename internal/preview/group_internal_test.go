package preview

import (
	"os/exec"
	"syscall"
	"testing"
)

// After the last kill (made before the leader is reaped), a late kill from the size cap
// or the deadline signals nothing, so it cannot reach a process that reused the group id.
func TestGroupLateKillDoesNothing(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Skip("sleep: ", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	g := &group{cmd: cmd, done: true} // as after last()
	g.kill()
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("a kill after the last one reached the process: %v", err)
	}
	g = &group{cmd: cmd}
	g.last()
	if err := cmd.Wait(); err == nil {
		t.Fatal("the last kill did not reach the process")
	}
	g.kill() // no process to signal, and no panic
}
