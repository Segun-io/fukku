package mediaserver

import (
	"context"
	"time"
)

// Pass runs one pass of the keeper (titleID 0: every due version), for tests.
func (k *Keeper) Pass(ctx context.Context, titleID int) { k.pass(ctx, titleID) }

// SetNow replaces the keeper's clock, for tests.
func (k *Keeper) SetNow(now func() time.Time) { k.now = now }

// PendingNudges is the number of nudges not served yet, for tests.
func (k *Keeper) PendingNudges() int { return len(k.nudges) }
