package eventsclient_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestNotifyClient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NotifyClient Suite")
}
