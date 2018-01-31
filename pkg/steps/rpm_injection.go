package steps

import (
	"fmt"

	buildapi "github.com/openshift/api/build/v1"
	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-operator/pkg/api"
)

func rpmInjectionDockerfile(from api.PipelineImageStreamTagReference, repo string) string {
	return fmt.Sprintf(`FROM %s:%s
RUN echo $'[built]\nname = Built RPMs\nbaseurl = http://%s\ngpgcheck = 0\nenabled = 0\n\n[origin-local-release]\nname = Built RPMs\nbaseurl = http://%s\ngpgcheck = 0\nenabled = 0' > /etc/yum.repos.d/built.repo`, PipelineImageStream, from, repo, repo)
}

type rpmImageInjectionStep struct {
	config      api.RPMImageInjectionStepConfiguration
	buildClient buildclientset.BuildInterface
	routeClient routeclientset.RouteInterface
	istClient   imageclientset.ImageStreamTagInterface
	jobSpec     *JobSpec
}

func (s *rpmImageInjectionStep) Run() error {
	route, err := s.routeClient.Get(RPMRepoName, meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not get Route for RPM server: %v", err)
	}
	dockerfile := rpmInjectionDockerfile(s.config.From, route.Spec.Host)
	build, err := s.buildClient.Create(buildFromSource(
		s.jobSpec, s.config.From, s.config.To,
		buildapi.BuildSource{
			Type:       buildapi.BuildSourceDockerfile,
			Dockerfile: &dockerfile,
		},
	))
	if ! errors.IsAlreadyExists(err) {
		return err
	}
	return waitForBuild(s.buildClient, build.Name)
}

func (s *rpmImageInjectionStep) Done() (bool, error) {
	return imageStreamTagExists(s.config.To, s.istClient)
}

func (s *rpmImageInjectionStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From), api.RPMRepoLink()}
}

func (s *rpmImageInjectionStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func RPMImageInjectionStep(config api.RPMImageInjectionStepConfiguration, buildClient buildclientset.BuildInterface, routeClient routeclientset.RouteInterface, istClient imageclientset.ImageStreamTagInterface, jobSpec *JobSpec) api.Step {
	return &rpmImageInjectionStep{
		config:      config,
		buildClient: buildClient,
		routeClient: routeClient,
		istClient:   istClient,
		jobSpec:     jobSpec,
	}
}
