package ibm

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const systemNamespace = "multi-platform-controller"

func TestIbm(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "IBM Suite")
}
