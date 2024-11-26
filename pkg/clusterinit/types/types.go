package types

import (
	"context"
	"os/exec"

	"github.com/ryanuber/go-glob"
	"github.com/sirupsen/logrus"

	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
)

const (
	ArchAMD64   string = "amd64"
	Arch_x86_64 string = "x86_64"
	ArchARM64   string = "arm64"
	ArchAARCH64 string = "aarch64"
)

// ToCoreOSStreamArch maps "our" architectures constants to arch strings that
// make sense in the CoreOS stream.json
func ToCoreOSStreamArch(arch string) string {
	if arch == ArchAMD64 {
		return Arch_x86_64
	}
	return arch
}

type ManifestGenerator interface {
	Name() string
	Skip() SkipStep
	Generate(context.Context, *logrus.Entry) (map[string][]interface{}, error)
	ExcludedManifests() ExcludeManifest
	Patches() []cinitmanifest.Patch
}

type SkipStep struct {
	Skip   bool   `json:"skip,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ExcludeManifest struct {
	Exclude []string `json:"exclude,omitempty"`
}

// Filter checks whether any exclusion paths match the path in input.
// It returns the matching glob expression, if any.
func (e *ExcludeManifest) Filter(path string) (string, bool) {
	for _, g := range e.Exclude {
		if glob.Glob(g, path) {
			return g, true
		}
	}
	return "", false
}

type Step interface {
	Run(ctx context.Context) error
	Name() string
}

type CmdBuilder func(ctx context.Context, program string, args ...string) *exec.Cmd
type CmdRunner func(cmd *exec.Cmd) error
