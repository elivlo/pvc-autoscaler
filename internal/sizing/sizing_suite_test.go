package sizing_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSizing(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sizing Suite")
}
