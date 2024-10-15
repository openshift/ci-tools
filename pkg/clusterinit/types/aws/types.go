package aws

import (
	"fmt"

	"github.com/openshift/ci-tools/pkg/clusterinit/types"
)

func AMIByArch(arch types.Architecture) (string, error) {
	switch arch {
	case types.ArchAMD64:
		return "ami-0545fae7edbbbf061", nil
	case types.ArchAARCH64:
		return "ami-0e9cdc0e85e0a6aeb", nil
	default:
		return "", fmt.Errorf("no ami for arch %s", arch)
	}
}
