//go:build linux

package merge

import "syscall"

// sysProcAttr puts mkvmerge in its own process group (killed as a whole) and kills it when
// the worker dies, so a mux cannot outlive the worker and write on after a restart.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
