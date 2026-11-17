package media

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"testing"
)

func TestWrap(t *testing.T) {
	name, args, err := Wrap(0, 0, "/bin/tool", []string{"a"})
	if err != nil || name != "/bin/tool" || !slices.Equal(args, []string{"a"}) {
		t.Fatalf("no wrapper: %q %v %v", name, args, err)
	}
	name, args, err = Wrap(10, 1<<30, "/bin/tool", []string{"a"})
	if err != nil {
		t.Skip("nice or prlimit is not installed")
	}
	want := []string{"-n", "10", "prlimit", "--as=1073741824", "--", "/bin/tool", "a"}
	if len(args) < 3 {
		t.Fatalf("args %v", args)
	}
	args[2] = "prlimit" // the resolved path differs per host
	if !slices.Equal(args, want) || name == "" {
		t.Fatalf("got %q %v, want nice %v", name, args, want)
	}
}

// TestHelperAllocate is the child of TestWrapLimitsMemory: it asks for 4 GiB of address space.
func TestHelperAllocate(t *testing.T) {
	if os.Getenv("FUKKU_TEST_ALLOCATE") == "" {
		t.Skip("child process")
	}
	sink = make([]byte, 4<<30) // reserved, never touched: no real memory is used
	sink[0] = 1
}

var sink []byte

// A tool past worker.toolMemoryLimit fails at once; it neither hangs nor takes the host's memory.
func TestWrapLimitsMemory(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name, args, err := Wrap(0, 2<<30, exe, []string{"-test.run=^TestHelperAllocate$"})
	if err != nil {
		t.Skip("prlimit is not installed")
	}
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Env = append(os.Environ(), "FUKKU_TEST_ALLOCATE=1")
	err = cmd.Run()
	if err == nil {
		t.Fatal("a 4 GiB allocation under a 2 GiB limit succeeded")
	}
	t.Logf("failed as expected: %v", err)

	// The same run without the limit works.
	name, args, _ = Wrap(0, 0, exe, []string{"-test.run=^TestHelperAllocate$"})
	cmd = exec.CommandContext(t.Context(), name, args...)
	cmd.Env = append(os.Environ(), "FUKKU_TEST_ALLOCATE=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("unlimited run failed: %v", err)
	}
}

func TestMemoryExhaustedText(t *testing.T) {
	if MemoryExhausted(nil, "out of memory", true) {
		t.Error("no error is not out of memory")
	}
	err := exec.CommandContext(t.Context(), "sh", "-c", "exit 1").Run()
	if MemoryExhausted(err, "Invalid data found", true) {
		t.Error("an ordinary failure is not out of memory")
	}
	if !MemoryExhausted(err, "av_malloc: Cannot allocate memory", true) {
		t.Error("ENOMEM text was missed")
	}
}

func TestMemoryExhaustedEvidence(t *testing.T) {
	err := exec.CommandContext(t.Context(), "sh", "-c", "kill -SEGV $$").Run()
	if !MemoryExhausted(err, "", true) {
		t.Error("a signal under a limit is a possibility")
	}
	if MemoryExhausted(err, "", false) {
		t.Error("a signal with no limit is not evidence of anything")
	}
	exit2 := exec.CommandContext(t.Context(), "sh", "-c", "exit 2").Run()
	if !MemoryExhausted(exit2, "Error: malloc() returned nullptr", false) {
		t.Error("mkvmerge's allocation failure was missed")
	}
	if MemoryExhausted(exit2, "Invalid argument", true) {
		t.Error("an ordinary exit is not out of memory")
	}
}

func TestLimitFailure(t *testing.T) {
	if err := LimitFailure("prlimit: failed to set the AS resource limit: Operation not permitted\n"); !errors.Is(err, ErrLimitNotSet) {
		t.Errorf("got %v", err)
	}
	if LimitFailure("Invalid data found when processing input") != nil {
		t.Error("a tool's own message is not prlimit's")
	}
}
