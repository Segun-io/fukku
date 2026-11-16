//go:build !linux

package preview

import "os/exec"

// run runs cmd and then kills what is left of its process group. The worker runs on
// Linux (run_linux.go), where the kill comes before the leader is reaped; elsewhere there
// is no portable way to wait without reaping.
func run(cmd *exec.Cmd, kill func()) error {
	err := cmd.Run()
	if cmd.Process != nil {
		kill()
	}
	return err
}
