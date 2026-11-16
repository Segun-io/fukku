package preview

import "time"

// Plan places up to n clips of the given length on a file of durationMs (the library
// file's video, as flow 2 compares it): the first at the start, the last ending at the
// end, the others evenly between them (3 clips: start, middle, end). One clip is the
// middle. Clips are never closer than half a clip's length, so a file not much longer
// than one clip gets fewer (a 21 s film and 20 s clips: one, not three that are almost the
// same), and one no longer than a clip gets a single clip of the whole file. nil when
// durationMs or n is not positive.
func Plan(durationMs int64, n int, length time.Duration) []Clip {
	if durationMs <= 0 || n <= 0 || length <= 0 {
		return nil
	}
	l := min(max(length.Milliseconds(), 1), durationMs)
	free := durationMs - l
	if step := l / 2; step > 0 {
		n = int(min(int64(n), free/step+1))
	}
	if n <= 1 {
		return []Clip{{StartMs: free / 2, LengthMs: l}}
	}
	clips := make([]Clip, 0, n)
	for i := range n {
		clips = append(clips, Clip{StartMs: free * int64(i) / int64(n-1), LengthMs: l})
	}
	return clips
}
