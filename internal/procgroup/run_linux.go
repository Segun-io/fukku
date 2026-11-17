package procgroup

import (
	"errors"

	"golang.org/x/sys/unix"
)

// Run starts the command and waits for it. The group's last kill is made once the leader
// has exited but before it is reaped, so nothing the tool started outlives the call and no
// kill reaches a reused id.
func (g *Group) Run() error {
	if err := g.cmd.Start(); err != nil {
		return err
	}
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, g.cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	g.Last()
	return g.cmd.Wait()
}
