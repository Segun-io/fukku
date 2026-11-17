package media

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Wrap returns the program and arguments that run bin with args at nice priority nice
// (worker.nice, 0 for none) under an address-space limit of memLimit bytes
// (worker.toolMemoryLimit, 0 for none): `nice -n N prlimit --core=0 --as=L -- bin args`. The limit
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
		// --core=0: a tool that dies past the limit (or on a crafted file) leaves no core dump in
		// the working directory; a dump would hold the tool's memory, the file it was reading included.
		args = append([]string{"--core=0", "--as=" + strconv.FormatInt(memLimit, 10), "--", bin}, args...)
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

// ErrLimitNotSet means prlimit could not set worker.toolMemoryLimit (for example the
// container's hard limit is lower): a problem of the deployment, not a verdict on the file.
var ErrLimitNotSet = errors.New("could not apply worker.toolMemoryLimit")

// LimitFailure returns ErrLimitNotSet (with prlimit's message) when stderr holds prlimit's
// own failure to set the limit, and nil otherwise.
func LimitFailure(stderr string) error {
	for line := range strings.SplitSeq(stderr, "\n") {
		if strings.HasPrefix(line, "prlimit: failed to set") {
			return fmt.Errorf("%w: %s", ErrLimitNotSet, strings.TrimSpace(line))
		}
	}
	return nil
}

// MemoryMessage reports whether a tool's messages show a failed allocation.
func MemoryMessage(stderr string) bool {
	s := strings.ToLower(stderr)
	for _, m := range []string{"cannot allocate memory", "out of memory", "bad_alloc",
		"malloc() returned", "memory allocation", "failed to allocate"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// MemoryExhausted reports whether a failed run (err from Cmd.Run or Wait, stderr the tool's
// messages) may have run out of memory. There is evidence when the tool reported a failed
// allocation, or when it died of SIGSEGV, SIGBUS, SIGABRT or SIGKILL while limited is true
// (an address-space limit or a container limit was in force). It is a possibility, not a
// proof: a crash on a crafted file looks the same, so callers word it as "may have".
func MemoryExhausted(err error, stderr string, limited bool) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	if MemoryMessage(stderr) {
		return true
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() && limited {
		switch ws.Signal() {
		case syscall.SIGKILL, syscall.SIGSEGV, syscall.SIGBUS, syscall.SIGABRT:
			return true
		}
	}
	return false
}

// MemoryDetail is the detail of a refusal for a tool that may have run out of memory.
func MemoryDetail(tool string) string {
	return tool + " failed and may have run out of memory (worker.toolMemoryLimit or the container's memory limit)"
}
