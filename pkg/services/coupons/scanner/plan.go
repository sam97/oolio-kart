package scanner

import (
	"runtime"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
)

const (
	// targetSlot is the memory each count worker aims for. Smaller slots
	// mean more workers and more, smaller buckets.
	targetSlot = 16 << 20

	minWriterSize = 4 << 10
	maxWriterSize = 256 << 10

	// maxBucketFiles bounds the files open at once during scatter, one per
	// (source, bucket).
	maxBucketFiles = 8192
)

// plan is how one scan spends its Budget.
type plan struct {
	buckets    int
	encoders   int
	writerSize int

	countWorkers int
	codesPerSlot int // codes each count worker can hold
}

// newPlan turns the budget into worker counts and buffer sizes.
//
// Count phase, all of Budget.Count:
//
//	countWorkers = clamp(Count / 16 MiB, 1, cores)    slot = Count / countWorkers
//
// Scatter phase:
//
//	encoders   = clamp(Encoders / perEncoder, 1, cores)
//	             perEncoder: a chunk's codes, encoded then grouped by bucket (8 + 8 bytes),
//	             plus a 4-byte bucket index each
//	buckets P  = ceil(estimated bucket bytes × 1.15 / slot)
//	             so bucket i, summed across all sources, fits one count slot
//	writerSize = clamp(Buckets / (sources × P), 4 KiB, 256 KiB)
//	             P is lowered if even 4 KiB writers would not fit
//
// Ex: Count = Encoders × 4 = Buckets × 4 = 192 MiB, 3 sources, 16 cores,
// 3.1 GB of 8–10 character codes, ~1 MiB chunks:
//
//	countWorkers = 12, slot = 16 MiB
//	encoders     = clamp(48 MiB / 2.2 MiB, 1, 16) = 16
//	P            = 2.77 GB × 1.15 / 16 MiB ≈ 190
//	writerSize   = 48 MiB / (3 × 190) ≈ 86 KiB
func newPlan(budget Budget, minLength, chunkSize int, sources []couponsource.Source) plan {
	cores := int64(runtime.GOMAXPROCS(0))
	countWorkers := clamp(budget.Count/targetSlot, 1, cores)
	slot := max(budget.Count/countWorkers, 8)

	perEncoder := (int64(chunkSize)/int64(minLength+1) + 1) * (8 + 8 + 4)
	encoders := clamp(budget.Encoders/perEncoder, 1, cores)

	// Every code takes at least minLength bytes plus a newline and becomes
	// 8 bytes, so this is an upper bound.
	var estimate int64
	for _, source := range sources {
		estimate += source.EstimatedSize()
	}
	bucketBytes := estimate / int64(minLength+1) * 8

	files := int64(len(sources))
	buckets := max(ceilDiv(bucketBytes*115/100, slot), 1)
	buckets = min(buckets, max(budget.Buckets/(files*minWriterSize), 1), max(maxBucketFiles/files, 1))

	return plan{
		buckets:      int(buckets),
		encoders:     int(encoders),
		writerSize:   int(clamp(budget.Buckets/(files*buckets), minWriterSize, maxWriterSize)),
		countWorkers: int(countWorkers),
		codesPerSlot: int(slot / 8),
	}
}

func clamp(value, low, high int64) int64 {
	return min(max(value, low), high)
}

func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}

// splitmix64 is the finaliser of the SplitMix64 generator. It mixes every
// input bit into every output bit, so hash % P spreads codes evenly whatever
// their alphabet or length.
func splitmix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
