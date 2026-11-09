package cache

import (
	"errors"
	"fmt"
	"os"
	"time"

	bolt "go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
)

// OpenTimeoutEnv overrides how long Open waits for another process to release
// the tree's cache db (a Go duration, e.g. "90s"). conformist#153.
const OpenTimeoutEnv = "CONFORMIST_CACHE_OPEN_TIMEOUT"

// DefaultOpenTimeout is how long Open waits for a busy cache db. The db is
// flocked for a holder's entire run, so on a host running many conformist
// processes over one tree root the wait must outlast a typical run (the old
// 1s limit failed under load, conformist#153).
const DefaultOpenTimeout = 60 * time.Second

// ErrBusy reports that another conformist process held the tree's cache db for
// the whole open timeout.
var ErrBusy = errors.New("cache db is held by another conformist process")

// openTimeout resolves the open wait from OpenTimeoutEnv, falling back to the
// default when unset or unparseable.
func openTimeout() time.Duration {
	raw := os.Getenv(OpenTimeoutEnv)
	if raw == "" {
		return DefaultOpenTimeout
	}

	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return DefaultOpenTimeout
	}

	return d
}

// openDB opens the bolt file, waiting up to the open timeout, and maps a
// timeout to ErrBusy so callers can tell contention from an unusable cache dir.
func openDB(path string) (*bolt.DB, error) {
	timeout := openTimeout()

	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: timeout})
	if err != nil {
		if errors.Is(err, berrors.ErrTimeout) {
			return nil, fmt.Errorf(
				"failed to open cache db at %s after waiting %s: %w (%w); set %s to wait longer",
				path, timeout, ErrBusy, err, OpenTimeoutEnv,
			)
		}

		return nil, fmt.Errorf("failed to open cache db at %s: %w", path, err)
	}

	return db, nil
}
