// Package couponstore keeps the published valid coupon codes, what they were
// built from, and the coupon settings.
package couponstore

import (
	"context"
	"encoding/json"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/models"
)

// Fingerprint is what a result was built from: the versions of the sources
// and the validity rules. A build is skipped when the published fingerprint
// is equal.
type Fingerprint struct {
	Sources []couponsource.Info
	Rules   string
}

func (fp Fingerprint) Equal(other Fingerprint) bool {
	return fp.Rules == other.Rules && couponsource.SameInfos(fp.Sources, other.Sources)
}

// Layout describes the bucket files a build left on disk, for a later
// incremental build to reuse.
type Layout struct {
	Hash      string         `json:"hash"`
	Buckets   int            `json:"buckets"`
	ByteOrder string         `json:"byteOrder"`
	Sources   []SourceLayout `json:"sources"`
}

type SourceLayout struct {
	Name  string `json:"name"`
	Dir   string `json:"dir"`
	Codes int64  `json:"codes"` // distinct codes across its buckets
}

// Manifest is published together with the codes.
type Manifest struct {
	Fingerprint
	Layout Layout
	Stats  json.RawMessage // how the build went, for operators
}

// Batch collects one result. Nothing is visible to readers until Commit. A
// Batch is used from one goroutine.
type Batch interface {
	// Add records a valid code. Codes arrive in no particular order.
	Add(code string) error

	// Commit replaces the published codes and manifest with this batch's,
	// atomically.
	Commit(ctx context.Context, manifest Manifest) error

	// Abort discards the batch. It is safe to call after Commit.
	Abort()
}

type Store interface {
	Begin(ctx context.Context) (Batch, error)

	// Published returns the fingerprint of the published codes. found is
	// false before the first build.
	Published(ctx context.Context) (fp Fingerprint, found bool, err error)
}

type Lookup interface {
	// Contains reports whether code is a published valid code. published is
	// false before the first build, when valid is meaningless.
	Contains(ctx context.Context, code string) (valid, published bool, err error)
}

type Settings interface {
	Settings(ctx context.Context) (models.CouponSettings, error)
}
