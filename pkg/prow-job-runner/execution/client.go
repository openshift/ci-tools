package execution

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/flagutil"
)

// NewClient creates a ProwJob client for the infrastructure cluster selected by opts.
func NewClient(opts *flagutil.KubernetesOptions) (client.Client, error) {
	if opts == nil {
		return nil, errors.New("kubernetes options are required")
	}
	config, err := opts.InfrastructureClusterConfig(false)
	if err != nil {
		return nil, fmt.Errorf("loading Prow cluster credentials: %w", err)
	}
	if config == nil || config.Host == "" {
		return nil, errors.New("no Prow infrastructure cluster configured for live submission")
	}
	scheme := runtime.NewScheme()
	if err := prowapi.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("registering ProwJob API: %w", err)
	}
	pjclient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating ProwJob client: %w", err)
	}
	return pjclient, nil
}
