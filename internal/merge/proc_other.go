//go:build !linux

package merge

import "syscall"

// sysProcAttr puts mkvmerge in its own process group, killed as a whole.
func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
