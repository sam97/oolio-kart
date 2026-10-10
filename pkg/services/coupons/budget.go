package coupons

import (
	"log/slog"

	"github.com/dustin/go-humanize"

	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
)

// MinMemoryLimit is the smallest cap the build can work within.
const MinMemoryLimit = 32 << 20

// Budget splits the coupon build's memory cap between its stages. Each stage
// turns its share into its own buffer sizes and goroutine counts.
//
// A quarter is held back for what no stage accounts for precisely: garbage
// awaiting the next GC cycle, goroutine stacks, the HTTP server and the
// served codes. The rest, work, is spent twice, because the build's two
// phases never run at the same time:
//
//	cap 256 MiB
//	├─ reserve ¼   64 MiB
//	└─ work    ¾  192 MiB
//	   scatter: read files, write bucket files      count: sort buckets, find valid codes
//	   ├─ Reader     48 MiB  decompressors, chunks   └─ Count 192 MiB  one slot per count worker
//	   ├─ Encoders   48 MiB  encode workers
//	   ├─ Buckets    48 MiB  bucket write buffers
//	   └─ slack      48 MiB  short-lived garbage
type Budget struct {
	Total   int64
	Reserve int64
	Reader  int64
	Scanner scanner.Budget
}

func NewBudget(total int64) Budget {
	work := total * 3 / 4
	quarter := work / 4
	return Budget{
		Total:   total,
		Reserve: total - work,
		Reader:  quarter,
		Scanner: scanner.Budget{Encoders: quarter, Buckets: quarter, Count: work},
	}
}

func (b Budget) LogValue() slog.Value {
	size := func(bytes int64) string { return humanize.IBytes(uint64(bytes)) }
	return slog.GroupValue(
		slog.String("total", size(b.Total)),
		slog.String("reserve", size(b.Reserve)),
		slog.String("reader", size(b.Reader)),
		slog.String("encoders", size(b.Scanner.Encoders)),
		slog.String("buckets", size(b.Scanner.Buckets)),
		slog.String("count", size(b.Scanner.Count)),
	)
}
