package cache_test

import (
	"errors"
	"testing"
	"time"

	"code.linenisgreat.com/conformist/walk/cache"
	"github.com/stretchr/testify/require"
)

// TestOpenWaitsForBusyHolder reproduces conformist#153: two openers on one
// tree root share one bbolt file, which is exclusively flocked for the holder's
// whole run. A second opener must wait for the holder to release rather than
// give up after a second.
func TestOpenWaitsForBusyHolder(t *testing.T) {
	as := require.New(t)

	t.Setenv(cache.OpenTimeoutEnv, "10s")

	root := t.TempDir()

	holder, err := cache.Open(root)
	as.NoError(err)

	released := make(chan struct{})

	go func() {
		// longer than the old hard-coded 1s timeout
		time.Sleep(2 * time.Second)
		_ = holder.Close()

		close(released)
	}()

	waiter, err := cache.Open(root)
	as.NoError(err, "second opener must wait out a busy holder")

	t.Cleanup(func() { _ = waiter.Close() })

	<-released
}

// TestOpenBusyIsDistinguishable asserts that when the holder never releases,
// the opener fails with ErrBusy and a message that names the cause.
func TestOpenBusyIsDistinguishable(t *testing.T) {
	as := require.New(t)

	t.Setenv(cache.OpenTimeoutEnv, "300ms")

	root := t.TempDir()

	holder, err := cache.Open(root)
	as.NoError(err)

	t.Cleanup(func() { _ = holder.Close() })

	_, err = cache.Open(root)
	as.Error(err)
	as.True(errors.Is(err, cache.ErrBusy), "got %v", err)
	as.ErrorContains(err, "failed to open cache db")
}
