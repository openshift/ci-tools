package release

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/candidate"
	"github.com/openshift/ci-tools/pkg/release/official"
	"github.com/openshift/ci-tools/pkg/release/prerelease"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

// ReleaseSource holds information to resolve a release pull-spec
// This is used to defer resolution in some cases until after a release is
// known to be required, based on the pruned step graph.  This structure holds
// the required configuration and clients until then.
type ReleaseSource interface {
	// Input provides the value for the `Inputs` method of steps
	// The return value may be empty if the pull-spec should not contribute to
	// the input.
	Input(ctx context.Context) (string, error)
	// PullSpec resolves the release pull-spec (if necessary) and returns it
	PullSpec(ctx context.Context) (string, error)
}

// NewReleaseSourceFromPullSpec creates a fixed, pre-computed source
func NewReleaseSourceFromPullSpec(s string) ReleaseSource {
	ret := fixedReleaseSource(s)
	return &ret
}

// NewReleaseSourceFromConfig uses the pull-spec of a published release payload
func NewReleaseSourceFromConfig(
	config *api.ReleaseConfiguration,
	httpClient release.HTTPClient,
) ReleaseSource {
	return &configurationReleaseSource{
		config: config,
		client: httpClient,
	}
}

// NewReleaseSourceFromClusterClaim determines the pull-spec for a cluster pool
func NewReleaseSourceFromClusterClaim(
	name string,
	claim *api.ClusterClaim,
	hiveClient ctrlruntimeclient.WithWatch,
) ReleaseSource {
	return &clusterClaimReleaseSource{
		testName: name,
		claim:    claim,
		client:   hiveClient,
	}
}

type fixedReleaseSource string

func (s fixedReleaseSource) PullSpec(ctx context.Context) (string, error) {
	return string(s), nil
}

func (s fixedReleaseSource) Input(ctx context.Context) (string, error) {
	return s.PullSpec(ctx)
}

type configurationReleaseSource struct {
	pullSpec string
	config   *api.ReleaseConfiguration
	client   release.HTTPClient
}

func (s configurationReleaseSource) PullSpec(
	ctx context.Context,
) (string, error) {
	if s.pullSpec == "" {
		if err := s.resolvePullSpec(); err != nil {
			return "", err
		}
	}
	return s.pullSpec, nil
}

func (s *configurationReleaseSource) Input(ctx context.Context) (string, error) {
	return s.PullSpec(ctx)
}

func (s *configurationReleaseSource) resolvePullSpec() (err error) {
	var spec string
	if c := s.config.Candidate; c != nil {
		spec, err = candidate.ResolvePullSpec(s.client, *c)
	} else if r := s.config.Release; r != nil {
		spec, _, err = official.ResolvePullSpecAndVersion(s.client, *r)
	} else if p := s.config.Prerelease; p != nil {
		spec, err = prerelease.ResolvePullSpec(s.client, *p)
	} else {
		panic("invalid release configuration")
	}
	if err != nil {
		return results.ForReason("resolving_release").ForError(fmt.Errorf("failed to resolve release %s: %w", s.config.Name, err))
	}
	s.pullSpec = spec
	logrus.Infof("Resolved release %s to %s", s.config.Name, s.pullSpec)
	return nil
}

type clusterClaimReleaseSource struct {
	pullSpec string
	testName string
	claim    *api.ClusterClaim
	client   ctrlruntimeclient.WithWatch
}

func (s clusterClaimReleaseSource) PullSpec(
	ctx context.Context,
) (string, error) {
	if s.pullSpec == "" {
		if err := s.resolvePullSpec(ctx); err != nil {
			return "", err
		}
	}
	return s.pullSpec, nil
}

func (s clusterClaimReleaseSource) Input(context.Context) (string, error) {
	return "", nil
}

func (s *clusterClaimReleaseSource) resolvePullSpec(ctx context.Context) error {
	pool, err := utils.ClusterPoolFromClaim(ctx, s.claim, s.client)
	if err != nil {
		return err
	}
	key := types.NamespacedName{Name: pool.Spec.ImageSetRef.Name}
	var set hivev1.ClusterImageSet
	if err := s.client.Get(ctx, key, &set); err != nil {
		return fmt.Errorf("failed to find cluster image set `%s` for cluster pool `%s`: %w", key.Name, pool.Name, err)
	}
	s.pullSpec = set.Spec.ReleaseImage
	logrus.Infof("Resolved release %s to %s", s.claim.ClaimRelease(s.testName).ReleaseName, s.pullSpec)
	return nil
}
