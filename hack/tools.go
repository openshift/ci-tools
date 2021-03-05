// +build tools

package hack

// Add tools that hack scripts depend on here, to ensure they are vendored.
import (
	_ "github.com/coreydaley/openshift-goimports"
	_ "github.com/polyfloyd/go-errorlint"

	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
