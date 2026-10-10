// Package couponstore keeps the valid coupon codes found by a scan, together
// with what they were built from, so a restart can skip the scan.
package couponstore

import (
	"context"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
)

// Fingerprint is what a result was built from: the versions of the sources
// and the validity rules. A result is reused only for an equal fingerprint.
type Fingerprint struct {
	Sources []couponsource.Info
	Rules   string
}

func (fp Fingerprint) Equal(other Fingerprint) bool {
	return fp.Rules == other.Rules && couponsource.SameInfos(fp.Sources, other.Sources)
}

// Batch collects one result. Nothing is visible to Load until Commit. A Batch
// is used from one goroutine.
type Batch interface {
	// Add records a valid code. Codes arrive in no particular order.
	Add(code string) error

	// Commit replaces the stored result with this one, atomically.
	Commit() error

	// Abort discards the batch.
	Abort()
}

type Writer interface {
	Begin(ctx context.Context, fp Fingerprint) (Batch, error)
}

type Store interface {
	Writer

	// Load returns the stored codes in sorted order if they were built from
	// fp. found is false when there is no result or it is out of date.
	Load(ctx context.Context, fp Fingerprint) (codes []string, found bool, err error)
}
