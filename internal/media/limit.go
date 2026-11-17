package media

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Wrap returns the program and arguments that run bin with args at nice priority nice
// (worker.nice, 0 for none) under an address-space limit of memLimit bytes
// (worker.toolMemoryLimit, 0 for none): `nice -n N prlimit --as=L -- bin args`. The limit
// is set by prlimit(1) from util-linux, in the child only, so the worker keeps its own. A
// tool that asks for more fails the allocation instead of growing until the host's OOM
// killer picks a victim. A wrapper that cannot be found is an error (a broken install),
// and one that cannot run bin exits 126 or 127, as nice does.
func Wrap(nice int, memLimit int64, bin string, args []string) (string, []string, error) {
	name := bin
	if memLimit > 0 {
		prlimit, err := exec.LookPath("prlimit")
		if err != nil {
			return "", nil, err
		}
		args = append([]string{"--as=" + strconv.FormatInt(memLimit, 10), "--", bin}, args...)
		name = prlimit
	}
	if nice > 0 {
		n, err := exec.LookPath("nice")
		if err != nil {
			return "", nil, err
		}
		args = append([]string{"-n", strconv.Itoa(nice), name}, args...)
		name = n
	}
	return name, args, nil
}

// MemoryExhausted reports whether a tool's failed run (err from Cmd.Run or Wait, stderr its
// last messages) looks like running out of memory under Wrap's limit, or under the
// container's: it died of SIGKILL (the cgroup OOM killer), SIGSEGV, SIGBUS or SIGABRT
// (an allocation that failed and was not checked, or std::bad_alloc), or said so.
func MemoryExhausted(err error, stderr string) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		switch ws.Signal() {
		case syscall.SIGKILL, syscall.SIGSEGV, syscall.SIGBUS, syscall.SIGABRT:
			return true
		}
	}
	s := strings.ToLower(stderr)
	return strings.Contains(s, "cannot allocate memory") || strings.Contains(s, "out of memory") ||
		strings.Contains(s, "bad_alloc") || strings.Contains(s, "memory allocation")
}

// MemoryDetail is the detail of a refusal for a tool that ran out of memory.
func MemoryDetail(tool string) string {
	return tool + " ran out of memory (worker.toolMemoryLimit)"
}
