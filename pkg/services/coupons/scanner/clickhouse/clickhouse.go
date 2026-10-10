// Package clickhouse is a scanner.Scanner that has a ClickHouse server find
// the valid codes. The server reads the coupon base files itself, so the same
// files must be mounted in its user_files folder:
//
//	coupons-job lists /data/couponbase{1,2,3}.gz  (for the fingerprint only)
//	ClickHouse reads  user_files/coupons/couponbase{1,2,3}.gz
//
//	load  INSERT INTO codes SELECT line, _file FROM file(…)     at most 1 GB
//	      codes: MergeTree, PARTITION BY cityHash64(code) % 64, ORDER BY code
//	merge OPTIMIZE TABLE codes FINAL: 350–470 parts ─▶ 64, one per partition
//	scan  SELECT code … GROUP BY code HAVING uniqExact(file) ≥ MinFiles
//	      in sort order, one code's state at a time            at most 256 MiB
//	      ──codes──▶ couponstore.Batch
//
// The table is dropped after each scan; ClickHouse is scratch space for the
// build, never where codes are published.
package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/models"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
)

// Name is how COUPONS_SCANNER and the build rules refer to this scanner.
const Name = "clickhouse"

const table = "codes"

// The session settings of each step, sent with its query. They were measured
// on the 313M-line coupon base files.
var (
	// loadSettings: with fewer threads and bigger blocks, the load fits in
	// its memory cap and leaves a few hundred parts, rather than about 20,000.
	loadSettings = ch.Settings{
		"max_threads":                4,
		"min_insert_block_size_rows": 4_000_000,
		"max_insert_block_size":      4_000_000,
		"max_memory_usage":           1_000_000_000,
	}

	// scanSettings: aggregating in sort order reads every part at once, so its
	// memory grows with the number of parts and with each part's block and
	// read buffer. Merged to 64 parts, it peaks around 86 MiB; unmerged, it
	// went past 256 MiB.
	scanSettings = ch.Settings{
		"optimize_aggregation_in_order": 1,
		"max_block_size":                4096,
		"max_read_buffer_size":          128 << 10,
		"max_memory_usage":              256 << 20,
	}
)

// cleanupTimeout bounds dropping the table after a scan, which runs even
// when the scan was cancelled.
const cleanupTimeout = 30 * time.Second

type Options struct {
	Conn driver.Conn

	// Dir is the folder holding the coupon base files, relative to the
	// server's user_files folder.
	Dir string

	// Settings decide which codes are valid; the discount is not used.
	Settings models.CouponSettings
}

type Scanner struct {
	opts Options
}

func New(opts Options) (*Scanner, error) {
	if opts.Conn == nil || opts.Dir == "" {
		return nil, errors.New("clickhouse scanner: Conn and Dir are required")
	}
	if err := checkName(opts.Dir); err != nil {
		return nil, fmt.Errorf("clickhouse scanner: Dir: %w", err)
	}
	settings := opts.Settings
	if settings.MinLength < 1 || settings.MaxLength < settings.MinLength || settings.MaxLength > models.CouponMaxLength {
		return nil, fmt.Errorf("clickhouse scanner: code lengths %d to %d are not allowed", settings.MinLength, settings.MaxLength)
	}
	if settings.MinFiles < 1 {
		return nil, errors.New("clickhouse scanner: MinFiles must be at least 1")
	}
	return &Scanner{opts: opts}, nil
}

// Scan loads sources into a table and adds the codes found in at least
// MinFiles of them to out. It does not use reader: the server reads the files.
func (s *Scanner) Scan(ctx context.Context, _ couponsource.Reader, sources []couponsource.Source, out couponstore.Batch) (scanner.Stats, error) {
	stats := scanner.Stats{Scanner: Name}
	if len(sources) == 0 {
		return stats, nil
	}
	path, err := s.path(sources)
	if err != nil {
		return stats, err
	}

	// A crashed run may have left the table behind.
	if err := s.opts.Conn.Exec(ctx, "DROP TABLE IF EXISTS "+table+" SYNC"); err != nil {
		return stats, fmt.Errorf("drop the old codes table: %w", err)
	}
	create := "CREATE TABLE " + table + ` (code String, file LowCardinality(String))
		ENGINE = MergeTree
		PARTITION BY cityHash64(code) % 64
		ORDER BY code`
	if err := s.opts.Conn.Exec(ctx, create); err != nil {
		return stats, fmt.Errorf("create the codes table: %w", err)
	}
	defer s.drop(ctx)

	start := time.Now()
	if err := s.load(ctx, path); err != nil {
		return stats, err
	}
	if err := s.opts.Conn.Exec(ctx, "OPTIMIZE TABLE "+table+" FINAL"); err != nil {
		return stats, fmt.Errorf("merge the codes table: %w", err)
	}
	stats.Scatter = time.Since(start)

	start = time.Now()
	stats.Codes, err = s.find(ctx, out)
	if err != nil {
		return stats, err
	}
	stats.Count = time.Since(start)
	return stats, nil
}

// load copies every line that could be a code into the table, with the name
// of the file it came from. Lines may end in \r\n as well as \n.
func (s *Scanner) load(ctx context.Context, path string) error {
	settings := s.opts.Settings
	insert := fmt.Sprintf(`INSERT INTO %s
		SELECT trim(TRAILING '\r' FROM line) AS code, _file
		FROM file('%s', LineAsString)
		WHERE length(code) BETWEEN %d AND %d
		  AND match(code, '^[0-9A-Za-z]+$')`,
		table, path, settings.MinLength, settings.MaxLength)
	if err := s.opts.Conn.Exec(ch.Context(ctx, ch.WithSettings(loadSettings)), insert); err != nil {
		return fmt.Errorf("load the coupon files: %w", err)
	}
	return nil
}

// find adds each code that appears in at least MinFiles files to out. A code
// repeated within one file counts once.
func (s *Scanner) find(ctx context.Context, out couponstore.Batch) (int, error) {
	query := fmt.Sprintf(`SELECT code FROM %s
		GROUP BY code
		HAVING uniqExact(file) >= %d`,
		table, s.opts.Settings.MinFiles)
	rows, err := s.opts.Conn.Query(ch.Context(ctx, ch.WithSettings(scanSettings)), query)
	if err != nil {
		return 0, fmt.Errorf("find the valid codes: %w", err)
	}
	defer rows.Close()

	codes := 0
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return codes, err
		}
		if err := out.Add(code); err != nil {
			return codes, err
		}
		codes++
	}
	if err := rows.Err(); err != nil {
		return codes, fmt.Errorf("find the valid codes: %w", err)
	}
	return codes, nil
}

// drop frees the table's disk space, even when ctx was cancelled.
func (s *Scanner) drop(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	// Best effort: the next scan drops the table first anyway.
	_ = s.opts.Conn.Exec(ctx, "DROP TABLE IF EXISTS "+table+" SYNC")
}

// path is the glob that matches exactly the sources, such as
// coupons/{couponbase1.gz,couponbase2.gz}.
func (s *Scanner) path(sources []couponsource.Source) (string, error) {
	names := make([]string, len(sources))
	for i, source := range sources {
		name := source.Info().Name
		if err := checkName(name); err != nil {
			return "", fmt.Errorf("coupon file %q: %w", name, err)
		}
		names[i] = name
	}
	if len(names) == 1 {
		return s.opts.Dir + "/" + names[0], nil
	}
	return s.opts.Dir + "/{" + strings.Join(names, ",") + "}", nil
}

// checkName rejects names that would change the meaning of the glob or end
// the SQL string they are quoted in.
func checkName(name string) error {
	if i := strings.IndexAny(name, `*?{},'\`); i >= 0 {
		return fmt.Errorf("the character %q is not supported in ClickHouse file paths", name[i])
	}
	return nil
}
