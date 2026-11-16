// Package fscheck checks the filesystems Fukku writes to. The package is owned by the
// /readyz work (#6); only Sentinels is here, added for the retention sweeper with the
// signature #6 specifies.
package fscheck

import (
	"fmt"
	"os"

	"github.com/segun-io/fukku/internal/config"
)

// Sentinels returns an error naming the first paths.sentinels file that is missing.
func Sentinels(p config.Paths) error {
	for _, s := range p.Sentinels {
		if _, err := os.Stat(s); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%s is missing", s)
			}
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}
