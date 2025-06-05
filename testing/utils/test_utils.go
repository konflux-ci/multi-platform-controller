package utils

import (
	"sort"
	"time"
)

// UniquenessTestStats holds the results from the PerformUniquenessAndPerformanceTest.
type UniquenessTestStats struct {
	GeneratedCount int           // Number of items successfully generated (TotalAttempts - ErrorCount).
	UniqueCount    int           // Number of unique items among successfully generated ones.
	DuplicateCount int           // Number of duplicate items found among successfully generated ones.
	ErrorCount     int           // Number of errors encountered during generation.
	ActualDuration time.Duration // Total time taken for the test.
}

// PerformUniquenessAndPerformanceTest runs a generator function multiple times,
// checks for uniqueness using the sort-and-scan method, counts errors,
// and measures performance.
func PerformUniquenessAndPerformanceTest(
	iterations int,
	generatorFunc func() (string, error),
) UniquenessTestStats {
	startTime := time.Now()
	// Allocate with capacity to potentially reduce reallocations
	generatedItems := make([]string, 0, iterations)
	var errorCount, duplicateCount int

	for i := 0; i < iterations; i++ {
		item, err := generatorFunc()
		if err != nil {
			errorCount++
			// Note: Logging of individual errors is deferred to the caller (Ginkgo test)
			// to keep this utility generic and not dependent on GinkgoWriter.
			continue
		}
		generatedItems = append(generatedItems, item)
	}

	actualDuration := time.Since(startTime)
	generatedCount := len(generatedItems)

	// Perform sort and duplicate check only if items were actually generated.
	if generatedCount > 0 {
		sort.Strings(generatedItems)
		for i := 1; i < generatedCount; i++ {
			if generatedItems[i] == generatedItems[i-1] {
				duplicateCount++
			}
		}
	}

	return UniquenessTestStats{
		GeneratedCount: generatedCount,
		UniqueCount:    generatedCount - duplicateCount,
		DuplicateCount: duplicateCount,
		ErrorCount:     errorCount,
		ActualDuration: actualDuration,
	}
}
