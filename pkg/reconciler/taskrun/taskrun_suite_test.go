package taskrun

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTaskrun(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Taskrun Suite")
}
