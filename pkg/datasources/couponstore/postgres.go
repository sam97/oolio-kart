package couponstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sam97/oolio-kart/pkg/models"
)

// Postgres keeps the codes in the tables of package postgres's migrations.
// It is a Store, a Lookup and Settings.
type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

// flushSize is how many codes a Batch buffers before copying them to the
// database, so a large result never sits in memory whole.
const flushSize = 4096

// Begin opens the transaction the batch commits in. Codes are copied into a
// temporary table first; Commit swaps them in, so readers keep seeing the
// previous codes until then.
func (p *Postgres) Begin(ctx context.Context) (Batch, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "CREATE TEMPORARY TABLE staged_codes (code text NOT NULL) ON COMMIT DROP"); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return &postgresBatch{ctx: ctx, tx: tx}, nil
}

type postgresBatch struct {
	ctx     context.Context // Begin's, since Add takes none
	tx      pgx.Tx
	pending [][]any
}

func (b *postgresBatch) Add(code string) error {
	b.pending = append(b.pending, []any{code})
	if len(b.pending) < flushSize {
		return nil
	}
	return b.flush(b.ctx)
}

func (b *postgresBatch) flush(ctx context.Context) error {
	if len(b.pending) == 0 {
		return nil
	}
	_, err := b.tx.CopyFrom(ctx, pgx.Identifier{"staged_codes"}, []string{"code"}, pgx.CopyFromRows(b.pending))
	b.pending = b.pending[:0]
	return err
}

func (b *postgresBatch) Commit(ctx context.Context, manifest Manifest) error {
	if err := b.flush(ctx); err != nil {
		return err
	}
	sources, err := json.Marshal(manifest.Sources)
	if err != nil {
		return err
	}
	layout, err := json.Marshal(manifest.Layout)
	if err != nil {
		return err
	}
	stats := manifest.Stats
	if stats == nil {
		stats = json.RawMessage("{}")
	}

	// DELETE rather than TRUNCATE: TRUNCATE would lock readers out until
	// the commit, while DELETE lets them read the previous codes.
	statements := []struct {
		sql  string
		args []any
	}{
		{"DELETE FROM coupon_codes", nil},
		{"INSERT INTO coupon_codes (code) SELECT DISTINCT code FROM staged_codes", nil},
		// clock_timestamp, since now() is when the transaction began, at the
		// start of the scan.
		{`INSERT INTO coupon_manifest (rules, sources, layout, stats, built_at) VALUES ($1, $2, $3, $4, clock_timestamp())
		  ON CONFLICT (id) DO UPDATE SET rules = $1, sources = $2, layout = $3, stats = $4, built_at = clock_timestamp()`,
			[]any{manifest.Rules, sources, layout, []byte(stats)}},
	}
	for _, statement := range statements {
		if _, err := b.tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			return err
		}
	}
	return b.tx.Commit(ctx)
}

func (b *postgresBatch) Abort() {
	// A no-op once committed. Rolls back even if Begin's context is done.
	b.tx.Rollback(context.Background())
}

func (p *Postgres) Published(ctx context.Context) (Fingerprint, bool, error) {
	var fp Fingerprint
	var sources []byte
	err := p.pool.QueryRow(ctx, "SELECT rules, sources FROM coupon_manifest").Scan(&fp.Rules, &sources)
	if errors.Is(err, pgx.ErrNoRows) {
		return Fingerprint{}, false, nil
	}
	if err != nil {
		return Fingerprint{}, false, err
	}
	if err := json.Unmarshal(sources, &fp.Sources); err != nil {
		return Fingerprint{}, false, fmt.Errorf("decode manifest sources: %w", err)
	}
	return fp, true, nil
}

func (p *Postgres) Contains(ctx context.Context, code string) (valid, published bool, err error) {
	err = p.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM coupon_codes WHERE code = $1), EXISTS (SELECT 1 FROM coupon_manifest)",
		code,
	).Scan(&valid, &published)
	return valid, published, err
}

func (p *Postgres) Settings(ctx context.Context) (models.CouponSettings, error) {
	var s models.CouponSettings
	err := p.pool.QueryRow(ctx,
		"SELECT min_length, max_length, min_files, discount_percent FROM coupon_settings",
	).Scan(&s.MinLength, &s.MaxLength, &s.MinFiles, &s.DiscountPercent)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, errors.New("coupon_settings has no row")
	}
	return s, err
}
