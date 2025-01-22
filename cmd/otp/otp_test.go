package main

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func loopCompare(s []string, target string) bool {
	// turns out simple comparison operators function the fastest in Go string comparison...
	for _, str := range s {
		if str == target {
			return true
		}
	}
	return false
}

var _ = Describe("OTP unit tests", func() {

	// A unit test for GenerateRandomString. Runs GenerateRandomString 10,000 times and checks if any
	// of them has been generatd before to validate randomness. This test takes 1.6 seconds but starts
	// to slow down to > 10 seconds at 50,000.
	// I think 10,000 runs is convincing enough...
	Describe("Testing GenerateRandomString", func() {

		It("GenerateRandomString should generate random strings without errors", func() {
			var testRounds = 10000

			var ideticalStrings = 0
			var errorCount = 0
			startTime := time.Now()
			randomStrings := make([]string, 0, testRounds)
			for i := 0; i < testRounds; i++ {
				randStr, err := GenerateRandomString(20)
				if err != nil {
					errorCount++
				} else if loopCompare(randomStrings, randStr) {
					ideticalStrings++
				} else {
					randomStrings = append(randomStrings, randStr)
				}

			}
			runTime := time.Since(startTime)
			fmt.Printf("Generated %d random strings in %s\n", testRounds, runTime)
			Expect(ideticalStrings).To(Equal(0))
			Expect(errorCount).To(Equal(0))
		})
	})

})
