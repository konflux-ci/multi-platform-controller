package mpcmetrics

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestPlatformMetrics(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Platform metrics Suite")
}
