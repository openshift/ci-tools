package certmanager

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
	"github.com/openshift/ci-tools/pkg/clusterinit/types"
	"github.com/openshift/ci-tools/pkg/kubernetes/portforward"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/fullstorydev/grpcurl"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
)

type GRPCClientConnFactory func(target string, opts ...grpc.DialOption) (conn *grpc.ClientConn, err error)

type certManagerGenerator struct {
	clusterInstall  *clusterinstall.ClusterInstall
	kubeClient      ctrlruntimeclient.Client
	portForwarder   portforward.PortForwarder
	grpcConnFactory GRPCClientConnFactory
	// For testing purpose only
	queryRedHatCatalog func(context.Context, GRPCClientConnFactory, string) (*Package, error)
}

func (s *certManagerGenerator) Name() string {
	return "cert-manager-operator"
}

func (s *certManagerGenerator) Skip() types.SkipStep {
	return s.clusterInstall.Onboard.CertManagerOperator.SkipStep
}

func (s *certManagerGenerator) ExcludedManifests() types.ExcludeManifest {
	return s.clusterInstall.Onboard.CertManagerOperator.ExcludeManifest
}

func (s *certManagerGenerator) Patches() []cinitmanifest.Patch {
	return s.clusterInstall.Onboard.CertManagerOperator.Patches
}

func (s *certManagerGenerator) Generate(ctx context.Context, log *logrus.Entry) (map[string][]interface{}, error) {
	if !s.clusterInstall.IsOCP() {
		log.Info("Not an OCP cluster, won't generate manifests")
		return map[string][]interface{}{}, nil
	}

	channel, version, err := s.getOperatorChannelAndVersion(ctx, log)
	if err != nil {
		return nil, err
	}

	basePath := onboard.CertManagerOperatorManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	pathToManifests := make(map[string][]interface{})
	pathToManifests[path.Join(basePath, "operator.yaml")] = operatorManifests(channel, version)
	pathToManifests[path.Join(basePath, "cert-issuer.yaml")] = certIssuerManifests()

	return pathToManifests, nil
}

// This procedure tries to emulate the following commands:
//
//	$ oc -n openshift-marketplace port-forward service/redhat-operators 50051 &>/dev/null &
//	$ grpcurl -plaintext -d '{"name": "openshift-cert-manager-operator"}' localhost:50051 'api.Registry/GetPackage' \
//		| jq '.defaultChannelName as $dc | .channels[] | select(.name==$dc)'
func (s *certManagerGenerator) getOperatorChannelAndVersion(ctx context.Context, log *logrus.Entry) (string, string, error) {
	pod, err := s.ensureRedHatCatalogPod(ctx, OpenshiftMarketplaceNS, RegistryCatalogPortInt)
	if err != nil {
		return "", "", fmt.Errorf("ensure pod: %w", err)
	}

	fwOpts := portforward.PortForwardOptions{
		PodName:     pod.Name,
		Namespace:   OpenshiftMarketplaceNS,
		Config:      s.clusterInstall.Config,
		StopChannel: make(chan struct{}),
		PodGetter: func(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
			p := &corev1.Pod{}
			nn := apimachinerytypes.NamespacedName{Namespace: namespace, Name: name}
			err := s.kubeClient.Get(ctx, nn, p)
			return p, err
		},
		Out:     io.Discard,
		ErrOut:  io.Discard,
		Address: []string{"localhost"},
		Ports:   []string{RegistryCatalogPort},
	}
	defer close(fwOpts.StopChannel)

	log.WithFields(logrus.Fields{"pod": pod.Name, "port": RegistryCatalogPort}).Info("Forwarding port")
	if err = <-portforward.Run(ctx, s.portForwarder, fwOpts); err != nil {
		return "", "", fmt.Errorf("port forward: %w", err)
	}

	operatorPackage, err := s.queryRedHatCatalog(ctx, s.grpcConnFactory, RegistryCatalogPort)
	if err != nil {
		return "", "", fmt.Errorf("query catalog: %w", err)
	}

	for _, c := range operatorPackage.Channels {
		if c.Name == operatorPackage.DefaultChannelName {
			log.WithFields(logrus.Fields{"channel": operatorPackage.DefaultChannelName, "version": c.CSVName}).
				Info("CertManager operator found")
			return c.Name, c.CSVName, nil
		}
	}

	return "", "", fmt.Errorf("cert-manager operator channel or CSVName not found")
}

// Ensure the pod that exposes info about the catalog is running and exposes the port we are going to
// forward traffic to.
func (s *certManagerGenerator) ensureRedHatCatalogPod(ctx context.Context, namespace string, port int32) (*corev1.Pod, error) {
	pods := &corev1.PodList{}
	requirement, err := labels.NewRequirement("olm.catalogSource", selection.Equals, []string{"redhat-operators"})
	if err != nil {
		return nil, fmt.Errorf("new requirement: %w", err)
	}

	err = s.kubeClient.List(ctx, pods, &ctrlruntimeclient.ListOptions{LabelSelector: labels.NewSelector().Add(*requirement)})
	if err != nil {
		return nil, fmt.Errorf("get pod (ns: %s - labels: %s): %w", namespace, requirement.String(), err)
	}

	if len(pods.Items) != 1 {
		return nil, fmt.Errorf("1 pod expected, got %d instead", len(pods.Items))
	}

	pod := &pods.Items[0]
	portExists := false

loop:
	for i := range pod.Spec.Containers {
		c := pod.Spec.Containers[i]
		for _, p := range c.Ports {
			if port == p.ContainerPort {
				portExists = true
				break loop
			}
		}
	}

	if !portExists {
		return nil, fmt.Errorf("port %d not found", port)
	}

	return pod, nil
}

// queryRedHatCatalog pull information regarding the cart-manager package from the Red Hat catalog.
func queryRedHatCatalog(ctx context.Context, clientConnFactory GRPCClientConnFactory, port string) (pack *Package, err error) {
	clientConn, err := clientConnFactory("127.0.0.1:"+port,
		grpc.WithAuthority("localhost"),
		grpc.WithUserAgent("cluster-init"),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		err = fmt.Errorf("new client: %w", err)
		return
	}
	defer func() {
		if err = clientConn.Close(); err != nil {
			err = fmt.Errorf("close: %s", err)
		}
	}()

	refClient := grpcreflect.NewClientAuto(ctx, clientConn)
	refClient.AllowMissingFileDescriptors()
	descSource := grpcurl.DescriptorSourceFromServer(ctx, refClient)

	options := grpcurl.FormatOptions{
		EmitJSONDefaultFields: false,
		IncludeTextSeparator:  true,
		AllowUnknownFields:    false,
	}
	reqReader := strings.NewReader(`{"name": "openshift-cert-manager-operator"}`)
	reqParser, formatter, err := grpcurl.RequestParserAndFormatter(grpcurl.Format("json"), descSource, reqReader, options)
	if err != nil {
		err = fmt.Errorf("req parser n formatter: %w", err)
		return
	}

	resBuf := bytes.NewBuffer([]byte{})
	resWriter := bufio.NewWriter(resBuf)
	handler := &grpcurl.DefaultEventHandler{
		Out:            resWriter,
		Formatter:      formatter,
		VerbosityLevel: 0,
	}

	symbol := "api.Registry/GetPackage"
	if err = grpcurl.InvokeRPC(ctx, descSource, clientConn, symbol, []string{}, handler, reqParser.Next); err != nil {
		err = fmt.Errorf("invoke rpc: %w", err)
		return
	}

	if handler.Status.Code() != codes.OK {
		err = fmt.Errorf("status code: %s", handler.Status.String())
		return
	}

	if err = resWriter.Flush(); err != nil {
		err = fmt.Errorf("flush response buffer: %w", err)
		return
	}

	pack = &Package{}
	if err = json.Unmarshal(resBuf.Bytes(), pack); err != nil {
		err = fmt.Errorf("unmarshal package: %w", err)
		return
	}

	return
}

func operatorManifests(channel, csvName string) []interface{} {
	return []interface{}{
		map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": "cert-manager-operator",
			},
		},
		map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1",
			"kind":       "OperatorGroup",
			"metadata": map[string]interface{}{
				"name":      "cert-manager-operator",
				"namespace": "cert-manager-operator",
			},
			"spec": map[string]interface{}{
				"targetNamespaces": []interface{}{
					"cert-manager-operator",
				},
			},
		},
		map[string]interface{}{
			"spec": map[string]interface{}{
				"channel":             channel,
				"installPlanApproval": "Automatic",
				"name":                "openshift-cert-manager-operator",
				"source":              "redhat-operators",
				"sourceNamespace":     "openshift-marketplace",
				"startingCSV":         csvName,
			},
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "Subscription",
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"operators.coreos.com/openshift-cert-manager-operator.cert-manager-operator": "",
				},
				"name":      "openshift-cert-manager-operator",
				"namespace": "cert-manager-operator",
			},
		},
		map[string]interface{}{
			"apiVersion": "operator.openshift.io/v1alpha1",
			"kind":       "CertManager",
			"metadata": map[string]interface{}{
				"name": "cluster",
			},
			"spec": map[string]interface{}{
				"unsupportedConfigOverrides": map[string]interface{}{
					"controller": map[string]interface{}{
						"args": []interface{}{
							"--dns01-recursive-nameservers=8.8.8.8:53",
							"--dns01-recursive-nameservers-only",
						},
					},
				},
			},
		},
	}
}

func certIssuerManifests() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "cert-issuer-aws",
			},
			"spec": map[string]interface{}{
				"acme": map[string]interface{}{
					"email": "openshift-ci-robot@redhat.com",
					"privateKeySecretRef": map[string]interface{}{
						"name": "cert-issuer-account-key",
					},
					"server": "https://acme-v02.api.letsencrypt.org/directory",
					"solvers": []interface{}{
						map[string]interface{}{
							"dns01": map[string]interface{}{
								"route53": map[string]interface{}{
									"hostedZoneID": "Z1T10JYHIP2LL9",
									"region":       "us-east-1",
									"secretAccessKeySecretRef": map[string]interface{}{
										"name": "cert-issuer",
										"key":  "AWS_SECRET_ACCESS_KEY",
									},
									"accessKeyID": "AKIAUVEZ656HEDJ456VW",
								},
							},
						},
					},
				},
			},
			"apiVersion": "cert-manager.io/v1",
			"kind":       "ClusterIssuer",
		},
		map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "ClusterIssuer",
			"metadata": map[string]interface{}{
				"name": "cert-issuer",
			},
			"spec": map[string]interface{}{
				"acme": map[string]interface{}{
					"email": "openshift-ci-robot@redhat.com",
					"privateKeySecretRef": map[string]interface{}{
						"name": "cert-issuer-account-key",
					},
					"server": "https://acme-v02.api.letsencrypt.org/directory",
					"solvers": []interface{}{
						map[string]interface{}{
							"dns01": map[string]interface{}{
								"cloudDNS": map[string]interface{}{
									"hostedZoneName": "origin-ci-ocp-public-dns",
									"project":        "openshift-ci-infra",
									"serviceAccountSecretRef": map[string]interface{}{
										"key":  "key.json",
										"name": "cert-issuer",
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func NewGenerator(clusterInstall *clusterinstall.ClusterInstall,
	kubeClient ctrlruntimeclient.Client,
	portForwarder portforward.PortForwarder,
	grpcConnFactory GRPCClientConnFactory) *certManagerGenerator {
	return &certManagerGenerator{
		clusterInstall:     clusterInstall,
		kubeClient:         kubeClient,
		portForwarder:      portForwarder,
		grpcConnFactory:    grpcConnFactory,
		queryRedHatCatalog: queryRedHatCatalog,
	}
}
