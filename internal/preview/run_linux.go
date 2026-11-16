package preview

import (
	"errors"
	"os/exec"

	"golang.org/x/sys/unix"
)

// run runs cmd, the leader of its own process group, and kills what is left of the group
// once the leader has exited but before it is reaped (waitid with WNOWAIT): until then the
// group id cannot be reused, so the kill reaches only what ffmpeg started.
func run(cmd *exec.Cmd, kill func()) error {
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
	kill()
	return cmd.Wait()
}
