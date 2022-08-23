package util

import (
	"fmt"
	"runtime"

	"github.com/sirupsen/logrus"
)

// ResolveMultiArchNamespaceFor returns the namespace name based on the os architecture
func ResolveMultiArchNamespaceFor(namespace string) string {
	var ret string
	arch := runtime.GOARCH
	logrus.Infof("Running on %s architecture", arch)
	if arch == "amd64" {
		ret = namespace
	} else {
		ret = fmt.Sprintf("%s-%s", namespace, arch)
	}
	logrus.Infof("Resolved multi-arch namespace for %s to %s for %s architecture", namespace, ret, arch)
	return ret
}
