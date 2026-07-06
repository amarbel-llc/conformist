package cache_test

import (
	"testing"

	"github.com/amarbel-llc/conformist/walk/cache"
	"github.com/stretchr/testify/require"
)

// TestAttestationRoundTrip covers the conformist#76 identity attestation store:
// a fresh cache has no attestation, WriteAttestation records one, ReadAttestation
// returns it, and a second write overwrites.
func TestAttestationRoundTrip(t *testing.T) {
	as := require.New(t)

	db, err := cache.Open(t.TempDir())
	as.NoError(err)

	t.Cleanup(func() { as.NoError(db.Close()) })

	// A fresh cache has no attestation recorded.
	got, err := cache.ReadAttestation(db)
	as.NoError(err)
	as.Empty(got)

	// Write then read back.
	as.NoError(cache.WriteAttestation(db, "deadbeef"))

	got, err = cache.ReadAttestation(db)
	as.NoError(err)
	as.Equal("deadbeef", got)

	// A second write overwrites the previous value.
	as.NoError(cache.WriteAttestation(db, "cafef00d"))

	got, err = cache.ReadAttestation(db)
	as.NoError(err)
	as.Equal("cafef00d", got)
}
