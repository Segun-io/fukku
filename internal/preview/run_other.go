//go:build !linux

package preview

import "os/exec"

// run runs cmd and then makes the group's last kill. The worker runs on Linux
// (run_linux.go), where that kill comes before the leader is reaped; elsewhere there is no
// portable way to wait without reaping.
func run(cmd *exec.Cmd, g *group) error {
	err := cmd.Run()
	g.last()
	return err
}
