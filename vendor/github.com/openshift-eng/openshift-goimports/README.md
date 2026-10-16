# openshift-goimports
Organizes Go imports according to OpenShift best practices

## Summary
Organizes Go imports into the following groups:
 - **standard** - Any of the Go standard library packages
 - **other** - Anything not specifically called out in this list
 - **kubernetes** - Anything that starts with `k8s.io`
 - **openshift** - Anything that starts with `github.com/openshift`
 - **module** - Anything that is part of the current module

### Example sorted import block
```
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	istorage "github.com/containers/image/v5/storage"
	"github.com/containers/image/v5/types"
	"github.com/containers/storage"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	restclient "k8s.io/client-go/rest"

	buildapiv1 "github.com/openshift/api/build/v1"
	buildscheme "github.com/openshift/client-go/build/clientset/versioned/scheme"
	buildclientv1 "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	"github.com/openshift/library-go/pkg/git"
	"github.com/openshift/library-go/pkg/serviceability"
	s2iapi "github.com/openshift/source-to-image/pkg/api"
	s2igit "github.com/openshift/source-to-image/pkg/scm/git"

	bld "github.com/openshift/builder/pkg/build/builder"
	"github.com/openshift/builder/pkg/build/builder/cmd/scmauth"
	"github.com/openshift/builder/pkg/build/builder/timing"
	builderutil "github.com/openshift/builder/pkg/build/builder/util"
	utillog "github.com/openshift/builder/pkg/build/builder/util/log"
	"github.com/openshift/builder/pkg/version"
)
```

## Installation
```
# Install using go get
$ go get -u github.com/openshift-eng/openshift-goimports
```

## Usage
```
Usage:
  openshift-goimports [flags]

Flags:
  -h, --help                             help for openshift-goimports
  -m, --module string                    The name of the go module. Example: github.com/example-org/example-repo (optional)
  -p, --path string                      The path to the go module to organize. Defaults to the current directory. (default ".") (optional)
  -d, --dry                              Dry run only, do not actually make any changes to files
  -v, --v Level                          number for the log level verbosity
```

## Examples
`openshift-goimports` will try to automatically determine the module using the `go.mod` file, if present, at the provided path location.

```
# Basic usage, command executed against current directory
$ openshift-goimports

# Basic usage with command executed in provided directory
$ openshift-goimports --module github.com/example-org/example-repo --path ~/go/src/example-org/example-repo
```
