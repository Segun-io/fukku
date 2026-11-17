package spool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/segun-io/fukku/internal/jobdir"
)

// Directories inside the spool. The worker's heartbeat sits next to them.
const (
	TasksDir   = "tasks"   // <id>.json, written by the web process: waiting
	RunningDir = "running" // <id>.json, renamed here by the worker when it claims a task
	ResultsDir = "results" // <id>.json, written by the worker when a task has finished
	CancelDir  = "cancel"  // <id>, an empty marker written by the web process: stop this task
)

// Error codes of a Result with status error.
const (
	CodeTimeout     = "timeout"      // the task ran past worker.taskTimeout
	CodeCancelled   = "cancelled"    // Cancel asked the worker to stop it
	CodeBadTask     = "bad_task"     // the task cannot be run as written (e.g. a malformed job id)
	CodeUnknownKind = "unknown_kind" // the worker has no handler for the kind
	CodeInternal    = "internal"     // the handler failed or panicked
	CodeBadResult   = "bad_result"   // Result found a result file it cannot read (too large, not JSON, not a file); the web side makes it, never the worker
)

// maxFileSize caps what is read from one spool file, so a broken or hostile peer cannot
// make the reader hold an unbounded file in memory.
const maxFileSize = 16 << 20

var idRE = regexp.MustCompile(`^[a-z0-9-]{1,96}$`)

// ValidID reports whether id is a well-formed task id.
func ValidID(id string) bool { return idRE.MatchString(id) }

// ValidateTask checks what Submit needs: a well-formed id, a kind, a well-formed job id and
// this protocol version (0 means this one).
func ValidateTask(t Task) error {
	switch {
	case !ValidID(t.ID):
		return fmt.Errorf("spool: malformed task id %q", t.ID)
	case t.Kind == "":
		return fmt.Errorf("spool: task %s has no kind", t.ID)
	case !jobdir.ValidID(t.Job):
		return fmt.Errorf("spool: task %s names a malformed job id", t.ID)
	case t.V != 0 && t.V != Version:
		return fmt.Errorf("spool: task %s has version %d, this binary writes %d", t.ID, t.V, Version)
	}
	return nil
}

// Dir is a spool directory. The web process uses it as a Queue; the worker uses Running,
// Waiting, Claim, CancelRequests and Finish, after LockWorker. Every file is written under a temporary name
// starting with a dot, in its final directory, and renamed into place; readers skip names
// that start with a dot. All access goes through an os.Root, so no file name, link or id
// can reach outside the spool. Dir is safe for concurrent use.
type Dir struct {
	root *os.Root
	now  func() time.Time

	mu      sync.Mutex
	ignored map[string]fileStamp // spool-relative path → the file as it was when reported
}

type fileStamp struct {
	size int64
	mod  time.Time
}

// Ignored is a file in tasks/ or running/ that is not a usable task. It is left where it is.
type Ignored struct {
	Path   string // spool-relative, e.g. "tasks/notes.txt"
	Reason string
}

var _ Queue = (*Dir)(nil)

// LockFile is the worker's lock, inside the spool directory.
const LockFile = "worker.lock"

// ErrLocked means another worker holds the spool's worker lock.
var ErrLocked = errors.New("spool: another worker is using this spool directory")

// LockWorker takes the spool's exclusive worker lock (flock on worker.lock), so that two
// workers never run the same tasks: two replicas, combined mode next to a worker, or an old
// worker still stopping while a new one starts. The lock is held until the returned file is
// closed or the process ends; it is ErrLocked while another worker holds it.
func (d *Dir) LockWorker() (io.Closer, error) {
	f, err := d.root.OpenFile(LockFile, os.O_RDWR|os.O_CREATE|syscall.O_NONBLOCK, 0o640)
	if err != nil {
		return nil, fmt.Errorf("spool: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("spool: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("spool: %s is not a regular file", LockFile)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("spool: lock %s: %w", LockFile, err)
	}
	return f, nil
}

// Open opens the spool directory dir, creating it and its subdirectories if needed.
func Open(dir string) (*Dir, error) {
	for _, sub := range []string{"", TasksDir, RunningDir, ResultsDir, CancelDir} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o750); err != nil {
			return nil, fmt.Errorf("spool: %w", err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("spool: %w", err)
	}
	return &Dir{root: root, now: time.Now, ignored: map[string]fileStamp{}}, nil
}

// Close releases the directory.
func (d *Dir) Close() error { return d.root.Close() }

func taskFile(dir, id string) string { return dir + "/" + id + ".json" }

// Submit writes tasks/<id>.json unless the task is waiting, running or has a result. It
// fills in V and, when it is zero, Created. One ID must not be submitted from two processes
// at the same time.
func (d *Dir) Submit(ctx context.Context, t Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateTask(t); err != nil {
		return err
	}
	// The order matters: a task only moves tasks → running → results, and its result is
	// written before its running file is removed, so it cannot slip past these checks.
	for _, dir := range []string{TasksDir, RunningDir, ResultsDir} {
		exists, err := d.exists(taskFile(dir, t.ID))
		if err != nil || exists {
			return err
		}
	}
	// Nothing with this id is waiting, running or finished, so a cancel marker for it is
	// left over from an earlier task (a Cancel that raced with the end of that task) and
	// must not stop this one.
	if err := d.remove(CancelDir + "/" + t.ID); err != nil {
		return err
	}
	t.V = Version
	if t.Created.IsZero() {
		t.Created = d.now().UTC()
	}
	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("spool: %w", err)
	}
	return d.write(TasksDir, t.ID+".json", data)
}

// Result reads results/<id>.json.
func (d *Dir) Result(ctx context.Context, id string) (Result, bool, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, false, err
	}
	if !ValidID(id) {
		return Result{}, false, fmt.Errorf("spool: malformed task id %q", id)
	}
	name := taskFile(ResultsDir, id)
	data, err := d.read(name)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, false, nil
	}
	if err != nil && !errors.Is(err, ErrUnreadable) {
		// A link or another odd entry that cannot be opened is as unusable as a large file.
		if fi, lerr := d.root.Lstat(name); lerr == nil && !fi.Mode().IsRegular() {
			err = fmt.Errorf("spool: %s is not a regular file (%s): %w", name, fi.Mode().Type(), ErrUnreadable)
		}
	}
	if errors.Is(err, ErrUnreadable) {
		return badResult(id, err), true, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	var r Result
	if err := json.Unmarshal(data, &r); err != nil {
		return badResult(id, fmt.Errorf("spool: %s: %w", name, err)), true, nil
	}
	if r.V > Version {
		// Written by a newer worker: this binary cannot read it, a matching web process
		// can after an upgrade. It stays where it is, and is not reported as a result.
		return Result{}, false, nil
	}
	if r.V != Version {
		return badResult(id, fmt.Errorf("spool: %s has version %d, this binary reads %d", name, r.V, Version)), true, nil
	}
	if r.ID != id {
		return badResult(id, fmt.Errorf("spool: %s is for task %q", name, r.ID)), true, nil
	}
	return r, true, nil
}

// badResult stands for a result file that cannot be read. It is a failed result, so that
// the caller fails the task once and acknowledges it (Ack removes the file without reading
// it); an error returned on every pass would leave the job unable to finish, cancel or
// expire.
func badResult(id string, cause error) Result {
	return Result{V: Version, ID: id, Status: StatusError,
		Error: &Error{Code: CodeBadResult, Message: cause.Error()}}
}

// Ack removes a finished task: running/<id>.json if a crash left it behind, cancel/<id>,
// and last results/<id>.json, so an interrupted Ack is completed by the next one. Without a
// result there is nothing to acknowledge and Ack does nothing.
func (d *Dir) Ack(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidID(id) {
		return fmt.Errorf("spool: malformed task id %q", id)
	}
	done, err := d.exists(taskFile(ResultsDir, id))
	if err != nil || !done {
		return err
	}
	// The claim goes first: a worker that crashed between writing the result and removing
	// its claim would otherwise run the acknowledged task again when it restarts.
	if err := d.remove(taskFile(RunningDir, id)); err != nil {
		return err
	}
	if err := d.remove(CancelDir + "/" + id); err != nil {
		return err
	}
	return d.remove(taskFile(ResultsDir, id))
}

// Cancel removes tasks/<id>.json, or, when the task is running, writes cancel/<id>.
func (d *Dir) Cancel(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidID(id) {
		return fmt.Errorf("spool: malformed task id %q", id)
	}
	err := d.root.Remove(taskFile(TasksDir, id))
	if err == nil {
		return d.syncDir(TasksDir)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("spool: %w", err)
	}
	// Not waiting: claimed already, finished, or unknown. A task is renamed into running/
	// before its waiting file disappears, so a claimed one is found here.
	running, err := d.exists(taskFile(RunningDir, id))
	if err != nil || !running {
		return err
	}
	if err := d.write(CancelDir, id, nil); err != nil {
		return err
	}
	// The task may have finished, and even been acknowledged, since the check. Then the
	// marker would stay behind: take it back unless the task is still there.
	for _, dir := range []string{RunningDir, ResultsDir} {
		if exists, err := d.exists(taskFile(dir, id)); err != nil || exists {
			return err
		}
	}
	return d.remove(CancelDir + "/" + id)
}

// Waiting returns the tasks in tasks/, oldest Created first. Files that are not usable
// tasks are left alone; each is returned in ignored once, and again only if it changes.
func (d *Dir) Waiting() (tasks []Task, ignored []Ignored, err error) {
	return d.scan(TasksDir)
}

// Running returns the claimed tasks that have no result, oldest Created first: after a
// crash, the worker runs them again. A claimed task that has a result was finished but not
// yet removed from running/; Running removes it.
func (d *Dir) Running() (tasks []Task, ignored []Ignored, err error) {
	all, ignored, err := d.scan(RunningDir)
	if err != nil {
		return nil, ignored, err
	}
	for _, t := range all {
		done, err := d.exists(taskFile(ResultsDir, t.ID))
		if err != nil {
			return nil, ignored, err
		}
		if !done {
			// Ack removes the claim before the result. If the claim is still there now,
			// the missing result was never written, rather than acknowledged meanwhile.
			claimed, err := d.exists(taskFile(RunningDir, t.ID))
			if err != nil {
				return nil, ignored, err
			}
			if claimed {
				tasks = append(tasks, t)
			}
			continue
		}
		if err := d.remove(taskFile(RunningDir, t.ID)); err != nil {
			return nil, ignored, err
		}
	}
	return tasks, ignored, nil
}

// Claim renames tasks/<id>.json into running/. ok is false when the task is no longer
// waiting (cancelled, or claimed by someone else).
func (d *Dir) Claim(id string) (ok bool, err error) {
	if !ValidID(id) {
		return false, fmt.Errorf("spool: malformed task id %q", id)
	}
	err = d.root.Rename(taskFile(TasksDir, id), taskFile(RunningDir, id))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("spool: claim %s: %w", id, err)
	}
	return true, errors.Join(d.syncDir(RunningDir), d.syncDir(TasksDir))
}

// CancelRequests returns the ids that have a cancel marker.
func (d *Dir) CancelRequests() ([]string, error) {
	entries, err := d.list(CancelDir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if ValidID(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// CancelRequested reports whether cancel/<id> exists.
func (d *Dir) CancelRequested(id string) (bool, error) {
	if !ValidID(id) {
		return false, fmt.Errorf("spool: malformed task id %q", id)
	}
	return d.exists(CancelDir + "/" + id)
}

// Finish writes results/<id>.json and then removes running/<id>.json. It fills in V. In
// this order a crash in between leaves a claimed task with a result, which Running
// cleans up; the other order could lose the task.
func (d *Dir) Finish(r Result) error {
	if !ValidID(r.ID) {
		return fmt.Errorf("spool: malformed task id %q", r.ID)
	}
	r.V = Version
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("spool: %w", err)
	}
	if err := d.write(ResultsDir, r.ID+".json", data); err != nil {
		return err
	}
	if err := d.remove(taskFile(RunningDir, r.ID)); err != nil {
		return err
	}
	return d.syncDir(RunningDir)
}

// scan reads every task in dir, the files that cannot be used reported once.
func (d *Dir) scan(dir string) ([]Task, []Ignored, error) {
	entries, err := d.list(dir)
	if err != nil {
		return nil, nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var (
		tasks   []Task
		ignored []Ignored
		present = map[string]bool{}
	)
	for _, e := range entries {
		name := dir + "/" + e.Name()
		info, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue // claimed, cancelled or finished since the listing
		}
		if err != nil {
			return nil, ignored, fmt.Errorf("spool: %w", err)
		}
		stamp := fileStamp{size: info.Size(), mod: info.ModTime()}
		present[name] = true
		if seen, ok := d.ignored[name]; ok && seen == stamp {
			continue
		}
		t, reason := d.parseTask(name, e.Name(), info)
		if reason != nil {
			if errors.Is(reason, fs.ErrNotExist) {
				continue
			}
			d.ignored[name] = stamp
			ignored = append(ignored, Ignored{Path: name, Reason: reason.Error()})
			continue
		}
		delete(d.ignored, name)
		tasks = append(tasks, t)
	}
	for name := range d.ignored {
		if strings.HasPrefix(name, dir+"/") && !present[name] {
			delete(d.ignored, name)
		}
	}
	slices.SortFunc(tasks, func(a, b Task) int {
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return tasks, ignored, nil
}

// parseTask reads one task file. The error says why the file is not a usable task.
func (d *Dir) parseTask(name, base string, info fs.FileInfo) (Task, error) {
	id, ok := strings.CutSuffix(base, ".json")
	if !ok || !ValidID(id) {
		return Task{}, errors.New("not a task file name (<id>.json)")
	}
	if !info.Mode().IsRegular() {
		return Task{}, errors.New("not a regular file")
	}
	data, err := d.read(name)
	if err != nil {
		return Task{}, err // fs.ErrNotExist: gone since the listing; the caller skips it
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return Task{}, fmt.Errorf("malformed JSON: %w", err)
	}
	if t.V != Version {
		return Task{}, fmt.Errorf("version %d, this binary reads %d", t.V, Version)
	}
	if t.ID != id {
		return Task{}, fmt.Errorf("the task's id %q does not match the file name", t.ID)
	}
	return t, nil
}

// list returns the entries of dir that do not start with a dot.
func (d *Dir) list(dir string) ([]fs.DirEntry, error) {
	f, err := openDir(d.root, dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("spool: %w", err)
	}
	return slices.DeleteFunc(entries, func(e fs.DirEntry) bool { return strings.HasPrefix(e.Name(), ".") }), nil
}

func (d *Dir) exists(name string) (bool, error) {
	_, err := d.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("spool: %w", err)
	}
	return true, nil
}

// ErrUnreadable wraps what ReadRegular reports of a file that is there but cannot be used:
// larger than the limit, or not a regular file.
var ErrUnreadable = errors.New("unreadable spool file")

func (d *Dir) read(name string) ([]byte, error) { return ReadRegular(d.root, name, maxFileSize) }

// ReadRegular reads name inside root, which must be a regular file of at most limit bytes.
// It never blocks on a FIFO or device that a peer put there: the file is opened
// non-blocking and checked on the open descriptor before anything is read. An error wraps
// fs.ErrNotExist when the file does not exist.
func ReadRegular(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("spool: %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("spool: %s is not a regular file (%s): %w", name, info.Mode().Type(), ErrUnreadable)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("spool: %s: %w", name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("spool: %s is larger than %d bytes: %w", name, limit, ErrUnreadable)
	}
	return data, nil
}

// openDir opens a directory inside root without blocking, whatever is found there.
func openDir(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, fmt.Errorf("spool: %w", err)
	}
	return f, nil
}

func (d *Dir) remove(name string) error {
	if err := d.root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("spool: %w", err)
	}
	return nil
}

// write puts data into dir/name atomically: a temporary file in dir, synced, renamed.
func (d *Dir) write(dir, name string, data []byte) error {
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	tmp := dir + "/.tmp-" + hex.EncodeToString(rnd[:])
	f, err := d.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return fmt.Errorf("spool: %w", err)
	}
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = d.root.Remove(tmp)
		return fmt.Errorf("spool: %w", err)
	}
	if err := d.root.Rename(tmp, dir+"/"+name); err != nil {
		_ = d.root.Remove(tmp)
		return fmt.Errorf("spool: %w", err)
	}
	return d.syncDir(dir)
}

// syncDir makes a rename or removal in dir durable.
func (d *Dir) syncDir(dir string) error {
	f, err := openDir(d.root, dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("spool: sync %s: %w", dir, err)
	}
	return nil
}
