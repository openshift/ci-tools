package clustermgmt

import "context"

type ClusterInstall struct {
}

type Step interface {
	Run(ctx context.Context) error
	Name() string
}
