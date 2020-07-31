package steps

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	routeapi "github.com/openshift/api/route/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	appsapi "k8s.io/api/apps/v1"
	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	appsclientset "k8s.io/client-go/kubernetes/typed/apps/v1"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
)

const (
	RPMRepoName    = "rpm-repo"
	AppLabel       = "app"
	TTLIgnoreLabel = "ci.openshift.io/ttl.ignore"
)

type rpmServerStep struct {
	config           api.RPMServeStepConfiguration
	deploymentClient appsclientset.DeploymentsGetter
	routeClient      routeclientset.RoutesGetter
	serviceClient    coreclientset.ServicesGetter
	istClient        imageclientset.ImageStreamTagsGetter
	jobSpec          *api.JobSpec
}

func (s *rpmServerStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (s *rpmServerStep) Run(ctx context.Context) error {
	return results.ForReason("serving_rpms").ForError(s.run(ctx))
}

func (s *rpmServerStep) run(ctx context.Context) error {
	ist, err := s.istClient.ImageStreamTags(s.jobSpec.Namespace()).Get(fmt.Sprintf("%s:%s", api.PipelineImageStream, s.config.From), meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not find source ImageStreamTag for RPM repo deployment: %w", err)
	}

	labelSet := defaultPodLabels(s.jobSpec)
	labelSet[AppLabel] = RPMRepoName
	labelSet[TTLIgnoreLabel] = "true"
	selectorSet := map[string]string{
		AppLabel: RPMRepoName,
	}
	commonMeta := meta.ObjectMeta{
		Name:      RPMRepoName,
		Namespace: s.jobSpec.Namespace(),
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
	one := int64(1)
	two := int32(2)
	deployment := &appsapi.Deployment{
		ObjectMeta: commonMeta,
		Spec: appsapi.DeploymentSpec{
			Replicas: &two,
			Selector: &meta.LabelSelector{
				MatchLabels: labelSet,
			},
			Template: coreapi.PodTemplateSpec{
				ObjectMeta: meta.ObjectMeta{
					Labels: labelSet,
				},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{
						Name:            RPMRepoName,
						Image:           ist.Image.DockerImageReference,
						ImagePullPolicy: coreapi.PullAlways,

						// SimpleHTTPServer is too simple - it can't handle threading. Use a threaded implementation
						// that binds multiple simple servers to the same port.
						Command: []string{"/bin/bash", "-c"},
						Args: []string{
							`
#!/bin/bash
cat <<END >>/tmp/serve.py
import time, threading, socket, SocketServer, BaseHTTPServer, SimpleHTTPServer

# Create socket
addr = ('', 8080)
sock = socket.socket (socket.AF_INET, socket.SOCK_STREAM)
sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
sock.bind(addr)
sock.listen(5)

# Launch multiple listeners as threads
class Thread(threading.Thread):
	def __init__(self, i):
		threading.Thread.__init__(self)
		self.i = i
		self.daemon = True
		self.start()
	def run(self):
		httpd = BaseHTTPServer.HTTPServer(addr, SimpleHTTPServer.SimpleHTTPRequestHandler, False)

		# Prevent the HTTP server from re-binding every handler.
		# https://stackoverflow.com/questions/46210672/
		httpd.socket = sock
		httpd.server_bind = self.server_close = lambda self: None

		httpd.serve_forever()
[Thread(i) for i in range(100)]
time.sleep(9e9)
END
python /tmp/serve.py
							`,
						},
						WorkingDir: api.RPMServeLocation,
						Ports: []coreapi.ContainerPort{{
							ContainerPort: 8080,
							Protocol:      coreapi.ProtocolTCP,
						}},
						ReadinessProbe: probe,
						LivenessProbe:  probe,
						Resources: coreapi.ResourceRequirements{
							Requests: coreapi.ResourceList{
								coreapi.ResourceCPU:    resource.MustParse("50m"),
								coreapi.ResourceMemory: resource.MustParse("50Mi"),
							},
						},
					}},
					TerminationGracePeriodSeconds: &one,
				},
			},
		},
	}
	if owner := s.jobSpec.Owner(); owner != nil {
		deployment.OwnerReferences = append(deployment.OwnerReferences, *owner)
	}

	if _, err := s.deploymentClient.Deployments(s.jobSpec.Namespace()).Create(context.TODO(), deployment, meta.CreateOptions{}); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create RPM repo server deployment: %w", err)
	}

	service := &coreapi.Service{
		ObjectMeta: commonMeta,
		Spec: coreapi.ServiceSpec{
			Ports: []coreapi.ServicePort{{
				Port:       8080,
				Protocol:   coreapi.ProtocolTCP,
				TargetPort: intstr.FromInt(8080),
			}},
			Selector: selectorSet,
		},
	}
	if owner := s.jobSpec.Owner(); owner != nil {
		service.OwnerReferences = append(service.OwnerReferences, *owner)
	}

	if _, err := s.serviceClient.Services(s.jobSpec.Namespace()).Create(context.TODO(), service, meta.CreateOptions{}); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create RPM repo server service: %w", err)
	}
	route := &routeapi.Route{
		ObjectMeta: commonMeta,
		Spec: routeapi.RouteSpec{
			To: routeapi.RouteTargetReference{
				Name: RPMRepoName,
			},
			Port: &routeapi.RoutePort{
				TargetPort: intstr.FromInt(8080),
			},
		},
	}
	if owner := s.jobSpec.Owner(); owner != nil {
		route.OwnerReferences = append(route.OwnerReferences, *owner)
	}

	if _, err := s.routeClient.Routes(s.jobSpec.Namespace()).Create(route); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create RPM repo server route: %w", err)
	}
	if err := waitForDeployment(ctx, s.deploymentClient.Deployments(s.jobSpec.Namespace()), deployment.Name); err != nil {
		return fmt.Errorf("could not wait for RPM repo server to deploy: %w", err)
	}
	return waitForRouteReachable(ctx, s.routeClient, s.jobSpec.Namespace(), route.Name, "http")
}

func waitForDeployment(ctx context.Context, client appsclientset.DeploymentInterface, name string) error {
	for {
		retry, err := waitForDeploymentOrTimeout(ctx, client, name)
		if err != nil {
			return fmt.Errorf("could not wait for deployment: %w", err)
		}
		if !retry {
			break
		}
	}

	return nil
}

func deploymentOK(b *appsapi.Deployment) bool {
	for _, condition := range b.Status.Conditions {
		if condition.Type == appsapi.DeploymentAvailable && condition.Status == coreapi.ConditionTrue {
			return true
		}
	}
	return false
}

func deploymentFailed(b *appsapi.Deployment) bool {
	for _, condition := range b.Status.Conditions {
		if condition.Type == appsapi.DeploymentProgressing && condition.Status == coreapi.ConditionFalse {
			return true
		}
	}
	return false
}

func deploymentReason(b *appsapi.Deployment) string {
	for _, condition := range b.Status.Conditions {
		if condition.Type == appsapi.DeploymentProgressing {
			return condition.Reason
		}
	}
	return ""
}

func waitForDeploymentOrTimeout(ctx context.Context, client appsclientset.DeploymentInterface, name string) (bool, error) {
	// First we set up a watcher to catch all events that happen while we check
	// the deployment status
	watcher, err := client.Watch(context.TODO(), meta.ListOptions{
		FieldSelector: fields.Set{"metadata.name": name}.AsSelector().String(),
		Watch:         true,
	})
	if err != nil {
		return false, fmt.Errorf("could not create watcher for deploymentconfig %s: %w", name, err)
	}
	defer watcher.Stop()

	done, err := currentDeploymentStatus(client, name)
	if err != nil {
		return false, fmt.Errorf("could not determine current deployment status: %w", err)
	}
	if done {
		return false, nil
	}
	ctxDone := ctx.Done()
	for {
		var event watch.Event
		var ok bool
		select {
		case <-ctxDone:
			return false, ctx.Err()
		case event, ok = <-watcher.ResultChan():
		}
		if !ok {
			// restart
			return true, nil
		}
		if deployment, ok := event.Object.(*appsapi.Deployment); ok {
			if deploymentOK(deployment) {
				return false, nil
			}
			if deploymentFailed(deployment) {
				return false, fmt.Errorf("the deployment config %s/%s failed with status %q", deployment.Namespace, deployment.Name, deploymentReason(deployment))
			}
		}
	}
}

func currentDeploymentStatus(client appsclientset.DeploymentInterface, name string) (bool, error) {
	list, err := client.List(context.TODO(), meta.ListOptions{FieldSelector: fields.Set{"metadata.name": name}.AsSelector().String()})
	if err != nil {
		return false, fmt.Errorf("could not list DeploymentConfigs: %w", err)
	}
	if len(list.Items) != 1 {
		return false, fmt.Errorf("could not find DeploymentConfig %s", name)
	}
	if deploymentOK(&list.Items[0]) {
		return true, nil
	}
	if deploymentFailed(&list.Items[0]) {
		return false, fmt.Errorf("the deployment config %s/%s failed with status %q", list.Items[0].Namespace, list.Items[0].Name, deploymentReason(&list.Items[0]))
	}

	return false, nil
}

func waitForRouteReachable(ctx context.Context, client routeclientset.RoutesGetter, namespace, name, scheme string, pathSegments ...string) error {
	host, err := admittedHostForRoute(client, namespace, name, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("could not determine admitted host for route: %w", err)
	}
	done := ctx.Done()
	for {
		u := &url.URL{Scheme: scheme, Host: host, Path: "/" + path.Join(pathSegments...)}
		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			return fmt.Errorf("could not create HTTP request: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Waiting for route to become available: %v", err)
			select {
			case <-done:
				return ctx.Err()
			case <-time.After(time.Second):
				continue
			}
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			log.Printf("Waiting for route to become available: %d", resp.StatusCode)
			select {
			case <-done:
				return ctx.Err()
			case <-time.After(time.Second):
				continue
			}
		}
		log.Printf("RPMs being served at %s", u)
		return nil
	}
}

func (s *rpmServerStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRPMs)}
}

func (s *rpmServerStep) Creates() []api.StepLink {
	return []api.StepLink{api.RPMRepoLink()}
}

func (s *rpmServerStep) rpmRepoURL() (string, error) {
	host, err := admittedHostForRoute(s.routeClient, s.jobSpec.Namespace(), RPMRepoName, time.Minute)
	if err != nil {
		return "", fmt.Errorf("unable to calculate rpm repo URL: %w", err)
	}
	return fmt.Sprintf("http://%s", host), nil
}

func (s *rpmServerStep) Provides() api.ParameterMap {
	var refs *v1.Refs
	if s.jobSpec.Refs != nil {
		refs = s.jobSpec.Refs
	} else if len(s.jobSpec.ExtraRefs) > 0 {
		refs = &s.jobSpec.ExtraRefs[0]
	}
	if refs != nil {
		rpmByOrgAndRepo := strings.Replace(fmt.Sprintf("RPM_REPO_%s_%s", strings.ToUpper(refs.Org), strings.ToUpper(refs.Repo)), "-", "_", -1)
		return api.ParameterMap{
			rpmByOrgAndRepo: s.rpmRepoURL,
		}
	}
	return nil
}

func (s *rpmServerStep) Name() string { return "[serve:rpms]" }

func (s *rpmServerStep) Description() string {
	return "Start a service that hosts the RPMs generated by this build"
}

func admittedHostForRoute(routeClient routeclientset.RoutesGetter, namespace, name string, timeout time.Duration) (string, error) {
	var repoHost string
	if err := wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		route, err := routeClient.Routes(namespace).Get(name, meta.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("could not get route %s: %w", name, err)
		}
		if host, ok := admittedRoute(route); ok {
			repoHost = host
			return len(repoHost) > 0, nil
		}
		return false, nil
	}); err != nil {
		return "", fmt.Errorf("could not retrieve route host: %w", err)
	}
	return repoHost, nil
}

func admittedRoute(route *routeapi.Route) (string, bool) {
	for _, ingress := range route.Status.Ingress {
		if len(ingress.Host) == 0 {
			continue
		}
		for _, condition := range ingress.Conditions {
			if condition.Type == routeapi.RouteAdmitted && condition.Status == coreapi.ConditionTrue {
				return ingress.Host, true
			}
		}
	}
	return "", false
}

func RPMServerStep(
	config api.RPMServeStepConfiguration,
	deploymentClient appsclientset.DeploymentsGetter,
	routeClient routeclientset.RoutesGetter,
	serviceClient coreclientset.ServicesGetter,
	istClient imageclientset.ImageStreamTagsGetter,
	jobSpec *api.JobSpec) api.Step {
	return &rpmServerStep{
		config:           config,
		deploymentClient: deploymentClient,
		routeClient:      routeClient,
		serviceClient:    serviceClient,
		istClient:        istClient,
		jobSpec:          jobSpec,
	}
}
