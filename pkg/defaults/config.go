package defaults

import (
	"time"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	templateapi "github.com/openshift/api/template/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/configresolver"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
)

type Config struct {
	Clients

	CIConfig                    *api.ReleaseBuildConfiguration
	GraphConf                   *api.GraphConfiguration
	JobSpec                     *api.JobSpec
	Templates                   []*templateapi.Template
	ParamFile                   string
	Promote                     bool
	ClusterConfig               *rest.Config
	PodPendingTimeout           time.Duration
	RequiredTargets             []string
	CloneAuthConfig             *steps.CloneAuthConfig
	PullSecret                  *coreapi.Secret
	PushSecret                  *coreapi.Secret
	Censor                      *secrets.DynamicCensor
	HiveKubeconfig              *rest.Config
	NodeName                    string
	NodeArchitectures           []string
	TargetAdditionalSuffix      string
	ManifestToolDockerCfg       string
	LocalRegistryDNS            string
	IntegratedStreams           map[string]*configresolver.IntegratedStream
	InjectedTest                bool
	EnableSecretsStoreCSIDriver bool
	MetricsAgent                *metrics.MetricsAgent
	SkippedImages               sets.Set[string]
	params                      *api.DeferredParameters
}

type Clients struct {
	LeaseClientEnabled bool
	LeaseClient        *lease.Client
	kubeClient         loggingclient.LoggingClient
	buildClient        steps.BuildClient
	templateClient     steps.TemplateClient
	podClient          kubernetes.PodClient
	hiveClient         ctrlruntimeclient.WithWatch
	httpClient         release.HTTPClient
}
