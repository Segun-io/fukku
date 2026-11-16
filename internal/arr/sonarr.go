package arr

import (
	"context"
	"errors"

	"github.com/segun-io/fukku/internal/config"
)

// ErrNotImplemented is returned by the Sonarr methods that issue #35 has not added yet.
var ErrNotImplemented = errors.New("arr: not implemented")

// sonarr is a stub until #35: the Importer methods work (they are shared), the series ones
// return ErrNotImplemented.
type sonarr struct {
	*client
}

// NewSonarr returns a SeriesManager for the Sonarr instance in cfg.
func NewSonarr(cfg config.Arr) SeriesManager {
	return &sonarr{newClient("sonarr", cfg)}
}

func (s *sonarr) Lookup(context.Context, string) ([]Series, error) { return nil, ErrNotImplemented }
func (s *sonarr) Series(context.Context, int) (Series, error)      { return Series{}, ErrNotImplemented }
func (s *sonarr) Add(context.Context, AddSeries) (Series, error)   { return Series{}, ErrNotImplemented }
func (s *sonarr) Episodes(context.Context, int) ([]Episode, error) { return nil, ErrNotImplemented }
func (s *sonarr) Busy(context.Context, int) (bool, error)          { return false, ErrNotImplemented }
func (s *sonarr) SetEpisodesMonitored(context.Context, []int, bool) error {
	return ErrNotImplemented
}
