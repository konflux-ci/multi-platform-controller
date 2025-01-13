package ibm

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestIbm(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Ibm Suite")
}
