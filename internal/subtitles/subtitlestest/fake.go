// Package subtitlestest has a fake subtitle manager for tests of code that hands files over.
package subtitlestest

import (
	"context"
	"io"
	"sync"

	"github.com/segun-io/fukku/internal/subtitles"
)

// Sent is one accepted or refused Upload call, with the file's bytes read.
type Sent struct {
	subtitles.Upload
	Data string
	Err  error
}

// Fake is a subtitle manager in memory. The zero value accepts every upload and shows it as
// written at the first look. It is safe for concurrent use.
type Fake struct {
	// VersionText is what Version returns.
	VersionText string
	// UploadErr, when set, is asked for each Upload call (n counts from 1) and may refuse it.
	UploadErr func(n int) error
	// WrittenAt, when set, says whether the history shows upload number upload (counting
	// accepted uploads from 1) as written at its poll-th look (from 1). Nil: at the first look.
	WrittenAt func(upload, poll int) bool
	// WrittenErr, when set, is returned by Written instead of an answer.
	WrittenErr func() error

	mu     sync.Mutex
	calls  int
	sent   []Sent
	polls  int // looks since the last accepted upload
	accept int // accepted uploads
}

var _ subtitles.SubtitleManager = (*Fake)(nil)

func (f *Fake) Version(context.Context) (string, error) {
	if f.VersionText == "" {
		return "1.6.2", nil
	}
	return f.VersionText, nil
}

func (f *Fake) Upload(_ context.Context, u subtitles.Upload) error {
	data, err := io.ReadAll(u.Content)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	s := Sent{Upload: u, Data: string(data)}
	s.Content = nil
	if f.UploadErr != nil {
		s.Err = f.UploadErr(f.calls)
	}
	f.sent = append(f.sent, s)
	if s.Err != nil {
		return s.Err
	}
	f.accept++
	f.polls = 0
	return nil
}

func (f *Fake) Written(context.Context, subtitles.Upload) (bool, error) {
	if f.WrittenErr != nil {
		if err := f.WrittenErr(); err != nil {
			return false, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	if f.WrittenAt == nil {
		return true, nil
	}
	return f.WrittenAt(f.accept, f.polls), nil
}

// Sent returns every Upload call so far, in order.
func (f *Fake) Sent() []Sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Sent(nil), f.sent...)
}
