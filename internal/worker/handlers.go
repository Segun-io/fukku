package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/extract"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/preview"
	"github.com/segun-io/fukku/internal/spool"
)

// Handlers returns the worker's task handlers: probe and libraryProbe (package media),
// preview (package preview), extract (package extract) and merge (package merge), with
// their settings taken from cfg.
func Handlers(cfg config.Config) map[string]Handler {
	probe := media.OptionsFrom(cfg)
	limits := extract.LimitsFrom(cfg)
	libraryRoots := slices.Clone(cfg.Paths.LibraryRoots)
	return map[string]Handler{
		spool.KindProbe: func(ctx context.Context, job, _ *os.Root, raw json.RawMessage) (any, error) {
			var args media.ProbeArgs
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			return media.Run(ctx, job, args, probe)
		},
		// The preview clips (#53) read the library file as the library probe does, and the
		// upload through the job directory; the clips go to the worker's directory of the job.
		spool.KindPreview: func(ctx context.Context, job, wdir *os.Root, raw json.RawMessage) (any, error) {
			var args preview.Args
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			out, err := preview.Run(ctx, job, wdir, libraryRoots, args, preview.OptionsFrom(cfg))
			if errors.Is(err, preview.ErrBadArgs) {
				return nil, fmt.Errorf("%w: %w", ErrBadTask, err)
			}
			return out, err
		},
		spool.KindMerge: mergeHandler(cfg),
		// The library probe (#52) does not use the job directory: it reads the library file,
		// confined to this process's own paths.libraryRoots (media.RunLibrary).
		spool.KindLibraryProbe: func(ctx context.Context, _, _ *os.Root, raw json.RawMessage) (any, error) {
			var args media.LibraryProbeArgs
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			return media.RunLibrary(ctx, libraryRoots, args, probe)
		},
		spool.KindExtract: func(ctx context.Context, job, wdir *os.Root, raw json.RawMessage) (any, error) {
			var args extract.Args
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			out, err := extract.Run(ctx, job, wdir, args, limits)
			if errors.Is(err, extract.ErrBadArgs) {
				return nil, fmt.Errorf("%w: %w", ErrBadTask, err)
			}
			return out, err
		},
	}
}

// decodeArgs decodes a task's args strictly: unknown fields or trailing data make it a
// bad task.
func decodeArgs(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: args: %w", ErrBadTask, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: args: trailing data", ErrBadTask)
	}
	return nil
}
