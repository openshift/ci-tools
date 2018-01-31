package steps

import (
	"fmt"
	"log"

	appsapi "github.com/openshift/api/apps/v1"
	routeapi "github.com/openshift/api/route/v1"
	appsclientset "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/intstr"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/ci-operator/pkg/api"
)

const (
	RPMRepoName = "rpm-repo"
	AppLabel    = "app"
)

type rpmServerStep struct {
	config           api.RPMServeStepConfiguration
	deploymentClient appsclientset.DeploymentConfigInterface
	routeClient      routeclientset.RouteInterface
	serviceClient    coreclientset.ServiceInterface
	istClient        imageclientset.ImageStreamTagInterface
	jobSpec          *JobSpec
}

func (s *rpmServerStep) Run() error {
	ist, err := s.istClient.Get(fmt.Sprintf("%s:%s", PipelineImageStream, s.config.From), meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not find source ImageStreamTag for RPM repo deployment: %v", err)
	}

	labelSet := map[string]string{
		PersistsLabel:    "true",
		JobLabel:         s.jobSpec.Job,
		BuildIdLabel:     s.jobSpec.BuildId,
		CreatedByCILabel: "true",
		AppLabel:         RPMRepoName,
	}
	commonMeta := meta.ObjectMeta{
		Name:      RPMRepoName,
		Namespace: s.jobSpec.Identifier(),
		Labels:    labelSet,
	}

	probe := &coreapi.Probe{
		Handler: coreapi.Handler{
			HTTPGet: &coreapi.HTTPGetAction{
				Path:   "/",
				Port:   intstr.FromInt(8080),
				Scheme: coreapi.URISchemeHTTP,
			},
		},
		InitialDelaySeconds: 1,
		PeriodSeconds:       10,
		SuccessThreshold:    1,
		TimeoutSeconds:      1,
	}
	deploymentConfig, err := s.deploymentClient.Create(&appsapi.DeploymentConfig{
		ObjectMeta: commonMeta,
		Spec: appsapi.DeploymentConfigSpec{
			Selector: labelSet,
			Template: &coreapi.PodTemplateSpec{
				ObjectMeta: meta.ObjectMeta{
					Labels: labelSet,
				},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{
						Name:            RPMRepoName,
						Image:           ist.Image.DockerImageReference,
						ImagePullPolicy: coreapi.PullAlways,
						Command:         []string{"/bin/python"},
						Args:            []string{"-m", "SimpleHTTPServer", "8080"},
						WorkingDir:      RPMServeLocation,
						Ports: []coreapi.ContainerPort{{
							ContainerPort: 8080,
							Protocol:      coreapi.ProtocolTCP,
						}},
						ReadinessProbe: probe,
						LivenessProbe:  probe,
					}},
				},
			},
		},
	})
	if ! kerrors.IsAlreadyExists(err) {
		return err
	}
	if _, err = s.serviceClient.Create(&coreapi.Service{
		ObjectMeta: commonMeta,
		Spec: coreapi.ServiceSpec{
			Ports: []coreapi.ServicePort{{
				Port:       8080,
				Protocol:   coreapi.ProtocolTCP,
				TargetPort: intstr.FromInt(8080),
			}},
			Selector: labelSet,
		},
	}); ! kerrors.IsAlreadyExists(err) {
		return err
	}
	if _, err = s.routeClient.Create(&routeapi.Route{
		ObjectMeta: commonMeta,
		Spec: routeapi.RouteSpec{
			To: routeapi.RouteTargetReference{
				Name: RPMRepoName,
			},
			Port: &routeapi.RoutePort{
				TargetPort: intstr.FromInt(8080),
			},
		},
	}); ! kerrors.IsAlreadyExists(err) {
		return err
	}
	return waitForDeployment(s.deploymentClient, deploymentConfig.Name)
}

func (s *rpmServerStep) Done() (bool, error) {
	return currentDeploymentStatus(s.deploymentClient, RPMRepoName)
}

func waitForDeployment(client appsclientset.DeploymentConfigInterface, name string) error {
	log.Printf("Waiting for DeploymentConfig %s to finish", name)
	for {
		retry, err := waitForDeploymentOrTimeout(client, name)
		if err != nil {
			return err
		}
		if !retry {
			break
		}
	}

	return nil
}

func deploymentOK(b *appsapi.DeploymentConfig) bool {
	for _, condition := range b.Status.Conditions {
		if condition.Type == appsapi.DeploymentAvailable && condition.Status == coreapi.ConditionTrue {
			return true
		}
	}
	return false
}

func deploymentFailed(b *appsapi.DeploymentConfig) bool {
	for _, condition := range b.Status.Conditions {
		if condition.Type == appsapi.DeploymentProgressing && condition.Status == coreapi.ConditionFalse {
			return true
		}
	}
	return false
}

func deploymentReason(b *appsapi.DeploymentConfig) string {
	for _, condition := range b.Status.Conditions {
		if condition.Type == appsapi.DeploymentProgressing {
			return condition.Reason
		}
	}
	return ""
}
func waitForDeploymentOrTimeout(client appsclientset.DeploymentConfigInterface, name string) (bool, error) {
	done, err := currentDeploymentStatus(client, name)
	if err != nil {
		return false, err
	}
	if done {
		return false, nil
	}

	watcher, err := client.Watch(meta.ListOptions{
		FieldSelector: fields.Set{"metadata.name": name}.AsSelector().String(),
		Watch:         true,
	})
	if err != nil {
		return false, err
	}
	defer watcher.Stop()

	for {
		event, ok := <-watcher.ResultChan()
		if !ok {
			// restart
			return true, nil
		}
		if deploymentConfig, ok := event.Object.(*appsapi.DeploymentConfig); ok {
			if deploymentOK(deploymentConfig) {
				return false, nil
			}
			if deploymentFailed(deploymentConfig) {
				return false, fmt.Errorf("the DeploymentConfig %s/%s failed with status %q", deploymentConfig.Namespace, deploymentConfig.Name, deploymentReason(deploymentConfig))
			}
		}
	}
}

func currentDeploymentStatus(client appsclientset.DeploymentConfigInterface, name string) (bool, error) {
	list, err := client.List(meta.ListOptions{FieldSelector: fields.Set{"metadata.name": name}.AsSelector().String()})
	if err != nil {
		return false, err
	}
	if len(list.Items) != 1 {
		return false, fmt.Errorf("could not find DeploymentConfig %s", name)
	}
	if deploymentOK(&list.Items[0]) {
		return true, nil
	}
	if deploymentFailed(&list.Items[0]) {
		return false, fmt.Errorf("the DeploymentConfig %s/%s failed with status %q", list.Items[0].Namespace, list.Items[0].Name, deploymentReason(&list.Items[0]))
	}

	return false, nil
}

func (s *rpmServerStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRPMs)}
}

func (s *rpmServerStep) Creates() []api.StepLink {
	return []api.StepLink{api.RPMRepoLink()}
}

func RPMServerStep(
	config api.RPMServeStepConfiguration,
	deploymentClient appsclientset.DeploymentConfigInterface,
	routeClient routeclientset.RouteInterface,
	serviceClient coreclientset.ServiceInterface,
	istClient imageclientset.ImageStreamTagInterface,
	jobSpec *JobSpec) api.Step {
	return &rpmServerStep{
		config:           config,
		deploymentClient: deploymentClient,
		routeClient:      routeClient,
		serviceClient:    serviceClient,
		istClient:        istClient,
		jobSpec:          jobSpec,
	}
}
