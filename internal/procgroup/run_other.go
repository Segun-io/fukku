//go:build !linux

package procgroup

// Run runs the command and then makes the group's last kill. The worker runs on Linux
// (run_linux.go), where that kill comes before the leader is reaped; elsewhere there is no
// portable way to wait without reaping.
func (g *Group) Run() error {
	err := g.cmd.Run()
	g.Last()
	return err
}
