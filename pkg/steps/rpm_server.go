package steps

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	appsapi "k8s.io/api/apps/v1"
	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
)

const (
	RPMRepoName    = "rpm-repo"
	AppLabel       = "app"
	TTLIgnoreLabel = "ci.openshift.io/ttl.ignore"
)

type rpmServerStep struct {
	config  api.RPMServeStepConfiguration
	client  loggingclient.LoggingClient
	jobSpec *api.JobSpec
}

func (s *rpmServerStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*rpmServerStep) Validate() error { return nil }

func (s *rpmServerStep) Run(ctx context.Context, o *api.RunOptions) error {
	return results.ForReason("serving_rpms").ForError(s.run(ctx))
}

func (s *rpmServerStep) run(ctx context.Context) error {
	ist := &imagev1.ImageStreamTag{}
	if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{
		Namespace: s.jobSpec.Namespace(),
		Name:      fmt.Sprintf("%s:%s", api.PipelineImageStream, s.config.From)},
		ist); err != nil {
		return fmt.Errorf("could not find source ImageStreamTag for RPM repo deployment: %w", err)
	}

	labelSet := labelsFor(s.jobSpec, map[string]string{AppLabel: RPMRepoName, TTLIgnoreLabel: "true"}, s.config.Ref)
	selectorSet := map[string]string{
		AppLabel: RPMRepoName,
	}
	commonMeta := meta.ObjectMeta{
		Name:      RPMRepoName,
		Namespace: s.jobSpec.Namespace(),
		Labels:    labelSet,
	}

	probe := &coreapi.Probe{
		ProbeHandler: coreapi.ProbeHandler{
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
	oneI64 := int64(1)
	oneI32 := int32(1)
	progressDeadline := int32(3600) // If a build farm is scaling up, provide plenty of time for pods to schedule
	deployment := &appsapi.Deployment{
		ObjectMeta: commonMeta,
		Spec: appsapi.DeploymentSpec{
			ProgressDeadlineSeconds: &progressDeadline,
			Replicas:                &oneI32,
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

if which python3 2> /dev/null; then
	# If python3 is available, use it
	cat <<END >/tmp/serve.py
import time, threading, socket, socketserver, http, http.server

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
		with socketserver.TCPServer(addr, http.server.SimpleHTTPRequestHandler, False) as httpd:
			# Prevent the HTTP server from re-binding every handler.
			# https://stackoverflow.com/questions/46210672/
			httpd.socket = sock
			httpd.server_bind = self.server_close = lambda self: None
			httpd.serve_forever()
[Thread(i) for i in range(100)]
time.sleep(9e9)
END
	python3 /tmp/serve.py
else
	# Else, fallback to python2
	cat <<END >/tmp/serve.py
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

fi
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
					TerminationGracePeriodSeconds: &oneI64,
				},
			},
		},
	}
	if owner := s.jobSpec.Owner(); owner != nil {
		deployment.OwnerReferences = append(deployment.OwnerReferences, *owner)
	}

	if err := s.client.Create(ctx, deployment); err != nil && !kerrors.IsAlreadyExists(err) {
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

	if err := s.client.Create(ctx, service); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create RPM repo server service: %w", err)
	}
	route := &routev1.Route{
		ObjectMeta: commonMeta,
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Name: RPMRepoName,
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromInt(8080),
			},
		},
	}
	if owner := s.jobSpec.Owner(); owner != nil {
		route.OwnerReferences = append(route.OwnerReferences, *owner)
	}

	if err := s.client.Create(ctx, route); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create RPM repo server route: %w", err)
	}
	if err := waitForDeployment(ctx, ctrlruntimeclient.NewNamespacedClient(s.client, s.jobSpec.Namespace()), deployment.Name); err != nil {
		return fmt.Errorf("could not wait for RPM repo server to deploy: %w", err)
	}
	return waitForRouteReachable(ctx, s.client, s.jobSpec.Namespace(), route.Name, "http")
}

func waitForDeployment(ctx context.Context, client ctrlruntimeclient.Client, name string) error {
	return waitForDeploymentOrTimeout(ctx, client, name)
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

func waitForDeploymentOrTimeout(ctx context.Context, client ctrlruntimeclient.Client, name string) error {
	done, err := currentDeploymentStatus(client, name)
	if err != nil {
		return fmt.Errorf("could not determine current deployment status: %w", err)
	}
	if done {
		return nil
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			deployment := &appsapi.Deployment{}
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: name}, deployment); err != nil {
				logrus.WithError(err).Error("Failed to get deployment")
			}
			if deploymentOK(deployment) {
				return nil
			}
			if deploymentFailed(deployment) {
				return fmt.Errorf("the deployment config %s/%s failed with status %q", deployment.Namespace, deployment.Name, deploymentReason(deployment))
			}
		}
	}
}

func currentDeploymentStatus(client ctrlruntimeclient.Client, name string) (bool, error) {
	deployment := &appsapi.Deployment{}
	if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Name: name}, deployment); err != nil {
		if kerrors.IsNotFound(err) {
			return false, fmt.Errorf("could not find Deployment %s", name)
		}
		return false, fmt.Errorf("could not get Deployment %s: %w", name, err)
	}
	if deploymentOK(deployment) {
		return true, nil
	}
	if deploymentFailed(deployment) {
		return false, fmt.Errorf("the deployment config %s/%s failed with status %q", deployment.Namespace, deployment.Name, deploymentReason(deployment))
	}

	return false, nil
}

func waitForRouteReachable(ctx context.Context, client ctrlruntimeclient.Client, namespace, name, scheme string, pathSegments ...string) error {
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
			logrus.WithError(err).Warn("Failed while waiting for route to become available.", err)
			select {
			case <-done:
				return ctx.Err()
			case <-time.After(time.Second):
				continue
			}
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			logrus.Infof("Waiting for route to become available: %d", resp.StatusCode)
			select {
			case <-done:
				return ctx.Err()
			case <-time.After(time.Second):
				continue
			}
		}
		logrus.Infof("RPMs being served at %s", u)
		return nil
	}
}

func (s *rpmServerStep) Requires() []api.StepLink {
	rpms := api.PipelineImageStreamTagReferenceRPMs
	if s.config.Ref != "" {
		rpms = api.PipelineImageStreamTagReference(fmt.Sprintf("%s-%s", rpms, s.config.Ref))
	}
	return []api.StepLink{api.InternalImageLink(rpms)}
}

func (s *rpmServerStep) Creates() []api.StepLink {
	return []api.StepLink{api.RPMRepoLink()}
}

func (s *rpmServerStep) rpmRepoURL() (string, error) {
	host, err := admittedHostForRoute(s.client, s.jobSpec.Namespace(), RPMRepoName, time.Minute)
	if err != nil {
		return "", fmt.Errorf("unable to calculate rpm repo URL: %w", err)
	}
	return fmt.Sprintf("http://%s", host), nil
}

func (s *rpmServerStep) Provides() api.ParameterMap {
	var refs []*v1.Refs
	if s.jobSpec.Refs != nil {
		refs = append(refs, s.jobSpec.Refs)
	}
	for i, ref := range s.jobSpec.ExtraRefs {
		orgRepo := fmt.Sprintf("%s.%s", ref.Org, ref.Repo)
		if s.config.Ref == "" || s.config.Ref == orgRepo {
			refs = append(refs, &s.jobSpec.ExtraRefs[i])
		}
	}
	if len(refs) == 0 {
		return nil
	}
	ret := make(api.ParameterMap)
	for _, ref := range refs {
		rpmByOrgAndRepo := strings.Replace(fmt.Sprintf("RPM_REPO_%s_%s", strings.ToUpper(ref.Org), strings.ToUpper(ref.Repo)), "-", "_", -1)
		ret[rpmByOrgAndRepo] = s.rpmRepoURL
	}
	return ret
}

func (s *rpmServerStep) Name() string { return s.config.TargetName() }

func (s *rpmServerStep) Description() string {
	return "Start a service that hosts the RPMs generated by this build"
}

func (s *rpmServerStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func admittedHostForRoute(client ctrlruntimeclient.Client, namespace, name string, timeout time.Duration) (string, error) {
	var repoHost string
	if err := wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		route := &routev1.Route{}
		if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, route); err != nil {
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

func admittedRoute(route *routev1.Route) (string, bool) {
	for _, ingress := range route.Status.Ingress {
		if len(ingress.Host) == 0 {
			continue
		}
		for _, condition := range ingress.Conditions {
			if condition.Type == routev1.RouteAdmitted && condition.Status == coreapi.ConditionTrue {
				return ingress.Host, true
			}
		}
	}
	return "", false
}

func RPMServerStep(
	config api.RPMServeStepConfiguration,
	client loggingclient.LoggingClient,
	jobSpec *api.JobSpec) api.Step {
	return &rpmServerStep{
		config:  config,
		client:  client,
		jobSpec: jobSpec,
	}
}
