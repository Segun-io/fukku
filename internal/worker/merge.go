package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/merge"
)

// mergeHandler runs the merge (#54, package merge): it reads the library file only inside
// this process's own paths.libraryRoots, the upload in the job directory, and writes only
// in the worker's directory of the job.
func mergeHandler(cfg config.Config) Handler {
	opt := merge.OptionsFrom(cfg)
	roots := slices.Clone(cfg.Paths.LibraryRoots)
	return func(ctx context.Context, job, wdir *os.Root, raw json.RawMessage) (any, error) {
		var args merge.Args
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		out, err := merge.Run(ctx, job, wdir, roots, args, opt)
		if errors.Is(err, merge.ErrBadArgs) {
			return nil, fmt.Errorf("%w: %w", ErrBadTask, err)
		}
		return out, err
	}
}
