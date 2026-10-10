package pebble

import (
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
)

const (
	// entryBytes is one code in a run: the packed code and its source
	// index, padded to 16 bytes.
	entryBytes = 16

	// targetRun is the run size each extra loader needs before it pays for
	// itself. Smaller runs mean more files, and the scan holds a block of
	// every file per worker.
	targetRun = 16 << 20

	maxLoaders  = 4
	minRunCodes = 64 << 10
	maxRunCodes = 8 << 20

	// blockSize is the size of a data block in the ingested files.
	blockSize = 32 << 10

	maxCache = 8 << 20

	// rangesPerWorker splits the scan finer than the worker count, so
	// workers that finish early take another range.
	rangesPerWorker = 4

	// samplesPerRun is how many codes each sorted run records for choosing
	// the scan's key ranges.
	samplesPerRun = 64
)

// plan is how one scan spends its Budget.
type plan struct {
	loaders  int
	runCodes int   // codes each loader sorts per run
	cache    int64 // Pebble's block cache
}

// newPlan turns the budget into loader and run sizes.
//
// Load phase, the scatter shares, Encoders + Buckets:
//
//	loaders  = clamp(min(cores / 4, load / 16 MiB), 1, 4)
//	runCodes = clamp(load / (loaders × 16 B), 64Ki, 8Mi)
//
// Scan phase, all of Count:
//
//	cache    = min(8 MiB, Count / 8)
//	workers  = clamp((Count − cache) / (files × 32 KiB), 1, cores)    see scanWorkers
//
// Ex: a 256 MiB cap (Encoders = Buckets = 48 MiB, Count = 192 MiB), 16 cores,
// 313M codes:
//
//	loaders  = min(4, 96 MiB / 16 MiB) = 4, runCodes = 96 MiB / 64 B ≈ 1.5M
//	files    ≈ 313M / 1.5M ≈ 210
//	workers  = clamp(184 MiB / (210 × 32 KiB), 1, 16) = 16, holding ~105 MiB
//
// At the 32 MiB minimum, one loader sorts runs of ~790K codes, making ~400
// files that a single worker scans in ~13 MiB.
func newPlan(budget scanner.Budget, cores int) plan {
	load := budget.Encoders + budget.Buckets
	loaders := clamp(min(int64(cores)/4, load/targetRun), 1, maxLoaders)
	return plan{
		loaders:  int(loaders),
		runCodes: int(clamp(load/(loaders*entryBytes), minRunCodes, maxRunCodes)),
		cache:    max(min(maxCache, budget.Count/8), 1<<20),
	}
}

// scanWorkers returns how many workers can scan files ingested files at once
// within the Count budget. Each worker holds about one block per file, so a
// single worker still goes over budget past (Count − cache) / blockSize
// files.
func scanWorkers(budget scanner.Budget, cache int64, files, cores int) int {
	perWorker := int64(max(files, 1)) * blockSize
	return int(clamp((budget.Count-cache)/perWorker, 1, int64(cores)))
}

func clamp(value, low, high int64) int64 {
	return min(max(value, low), high)
}
