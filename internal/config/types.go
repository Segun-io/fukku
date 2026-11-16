package config

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"
)

// Secret is a string that never prints. Use Reveal to get the value.
type Secret string

func (s Secret) Reveal() string { return string(s) }

func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return "[redacted]"
}

func (s Secret) GoString() string { return s.String() }

func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// ByteSize is a size in bytes. It parses plain numbers and IEC/SI suffixes: 16MiB, 80GiB, 500MB.
type ByteSize int64

var byteUnits = []struct {
	suffix string
	mult   int64
}{
	{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
	{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}, {"B", 1},
}

func ParseByteSize(s string) (ByteSize, error) {
	s = strings.TrimSpace(s)
	for _, u := range byteUnits {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			if err != nil || f < 0 {
				return 0, fmt.Errorf("invalid size %q", s)
			}
			v := f * float64(u.mult)
			if v > math.MaxInt64 {
				return 0, fmt.Errorf("size %q is too large", s)
			}
			return ByteSize(v), nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q (use a number of bytes or a suffix such as MiB, GiB)", s)
	}
	return ByteSize(n), nil
}

func (b *ByteSize) UnmarshalText(text []byte) error {
	v, err := ParseByteSize(string(text))
	if err != nil {
		return err
	}
	*b = v
	return nil
}

func (b ByteSize) MarshalText() ([]byte, error) { return []byte(b.String()), nil }

func (b ByteSize) String() string {
	for _, u := range byteUnits[:4] {
		if b != 0 && int64(b)%u.mult == 0 {
			return strconv.FormatInt(int64(b)/u.mult, 10) + u.suffix
		}
	}
	return strconv.FormatInt(int64(b), 10)
}

// Duration is a time.Duration that also accepts a "d" (24 h) suffix, e.g. 14d.
type Duration time.Duration

func ParseDuration(s string) (Duration, error) {
	s = strings.TrimSpace(s)
	if num, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(num, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (examples: 500ms, 60s, 2h, 14d)", s)
	}
	return Duration(d), nil
}

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d Duration) String() string {
	td := time.Duration(d)
	if td >= 24*time.Hour && td%(24*time.Hour) == 0 {
		return strconv.FormatInt(int64(td/(24*time.Hour)), 10) + "d"
	}
	// time.Duration prints 1h0m0s and 2m0s; drop the zero parts.
	s := td.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

func (d Duration) D() time.Duration { return time.Duration(d) }
