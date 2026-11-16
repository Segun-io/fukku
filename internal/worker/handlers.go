package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/extract"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/spool"
)

// Handlers returns the worker's task handlers: probe (package media) and extract (package
// extract), with their settings taken from cfg.
func Handlers(cfg config.Config) map[string]Handler {
	probe := media.OptionsFrom(cfg)
	limits := extract.LimitsFrom(cfg)
	return map[string]Handler{
		spool.KindProbe: func(ctx context.Context, job *os.Root, raw json.RawMessage) (any, error) {
			var args media.ProbeArgs
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			return media.Run(ctx, job, args, probe)
		},
		spool.KindExtract: func(ctx context.Context, job *os.Root, raw json.RawMessage) (any, error) {
			var args extract.Args
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			out, err := extract.Run(ctx, job, args, limits)
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
