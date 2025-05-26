// "Assisted-by: TAG"
package main

import (
	"fmt"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OTP unit tests", func() {

	// A unit test for GenerateRandomString. Runs GenerateRandomString 10,000 times and checks if any
	// of them has been generatd before to validate randomness. This test takes 1.6 seconds but starts
	// to slow down to > 10 seconds at 50,000.
	// I think 10,000 runs is convincing enough...
	Describe("Testing GenerateRandomString", func() {

		It("GenerateRandomString should generate random strings without errors", func() {
			var testRounds = 100000

			var ideticalStrings = 0
			var errorCount = 0
			startTime := time.Now()
			randomStrings := make([]string, 0, testRounds)
			for i := 0; i < testRounds; i++ {
				randStr, err := GenerateRandomString(20)
				if err != nil {
					errorCount++
				} else {
					randomStrings = append(randomStrings, randStr)
				}
			}
			sort.Strings(randomStrings)
			for i := 1; i < len(randomStrings); i++ {
				if randomStrings[i] == randomStrings[i-1] {
					ideticalStrings++
				}
			}
			runTime := time.Since(startTime)
			fmt.Printf("Generated %d random strings in %s\n", testRounds, runTime)
			Expect(ideticalStrings).To(Equal(0))
			Expect(errorCount).To(Equal(0))
		})
	})

})
