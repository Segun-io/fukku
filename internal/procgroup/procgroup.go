// Package procgroup runs an external tool as the leader of its own process group and kills
// the group without ever signalling a reused id.
//
// A process group id is the leader's PID. Once the leader is reaped the id may be given to
// an unrelated process, so a kill(-pid) made after cmd.Run() has returned could hit
// something else. Run therefore waits for the leader to exit without reaping it (waitid
// with WNOWAIT), makes the group's last kill, and only then reaps. Every kill after that
// does nothing.
package procgroup

import (
	"os/exec"
	"sync"
	"syscall"
)

// Group is the process group of a command's leader.
type Group struct {
	cmd *exec.Cmd

	mu   sync.Mutex
	done bool
}

// New prepares cmd to lead its own process group (Setpgid, keeping any other SysProcAttr
// fields the caller set) and makes the context's cancellation kill the group through
// Kill. Call it before Run.
func New(cmd *exec.Cmd) *Group {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	g := &Group{cmd: cmd}
	cmd.Cancel = func() error { g.Kill(); return nil }
	return g
}

// Kill sends SIGKILL to the group (the size cap, the deadline and a cancel all do), unless
// the last kill was made.
func (g *Group) Kill() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.signal()
}

// Last makes the last kill: whatever the tool left in the group goes, and later kills do
// nothing.
func (g *Group) Last() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.signal()
	g.done = true
}

func (g *Group) signal() {
	if !g.done && g.cmd.Process != nil {
		_ = syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL)
	}
}
