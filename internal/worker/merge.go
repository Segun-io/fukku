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
// this process's own paths.libraryRoots and writes only in the task's job directory.
func mergeHandler(cfg config.Config) Handler {
	opt := merge.OptionsFrom(cfg)
	roots := slices.Clone(cfg.Paths.LibraryRoots)
	return func(ctx context.Context, job *os.Root, raw json.RawMessage) (any, error) {
		var args merge.Args
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		out, err := merge.Run(ctx, job, roots, args, opt)
		if errors.Is(err, merge.ErrBadArgs) {
			return nil, fmt.Errorf("%w: %w", ErrBadTask, err)
		}
		return out, err
	}
}
