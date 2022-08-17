package util

import (
	"fmt"
	"runtime"
)

// ResolveMultiArchNamespaceFor returns the namespace name based on the os architecture
func ResolveMultiArchNamespaceFor(namespace string) string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		return namespace
	}
	return fmt.Sprintf("%s-%s", namespace, arch)
}
