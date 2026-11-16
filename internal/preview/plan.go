package preview

import "time"

// Plan places n clips of the given length on a file of durationMs (the library file's
// video, as flow 2 compares it): the first at the start, the last ending at the end, the
// others evenly between them (3 clips: start, middle, end). One clip is the middle. A file
// no longer than one clip gets a single clip of the whole file. nil when durationMs or n
// is not positive.
func Plan(durationMs int64, n int, length time.Duration) []Clip {
	if durationMs <= 0 || n <= 0 || length <= 0 {
		return nil
	}
	l := min(length.Milliseconds(), durationMs)
	if l <= 0 {
		l = 1
	}
	free := durationMs - l
	switch {
	case free == 0:
		return []Clip{{StartMs: 0, LengthMs: l}}
	case n == 1:
		return []Clip{{StartMs: free / 2, LengthMs: l}}
	}
	clips := make([]Clip, 0, n)
	for i := range n {
		start := free * int64(i) / int64(n-1)
		if len(clips) > 0 && clips[len(clips)-1].StartMs == start {
			continue // a file barely longer than one clip
		}
		clips = append(clips, Clip{StartMs: start, LengthMs: l})
	}
	return clips
}
