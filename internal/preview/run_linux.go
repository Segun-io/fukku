package preview

import (
	"errors"
	"os/exec"

	"golang.org/x/sys/unix"
)

// run runs cmd, the leader of its own process group, and makes the group's last kill once
// the leader has exited but before it is reaped (waitid with WNOWAIT): until then the
// group id cannot be reused, so that kill, and any before it, reaches only what ffmpeg
// started. The size cap's and the deadline's kills that come later do nothing.
func run(cmd *exec.Cmd, g *group) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	g.last()
	return cmd.Wait()
}
