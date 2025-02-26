package multi_stage

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	rbacapi "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	csiapi "sigs.k8s.io/secrets-store-csi-driver/apis/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/util"
)

var (
	// uidRangeRegexp parses the base UID from a `${base}/${size}` UID range.
	// This is the format of the `openshift.io/sa.scc.uid-range` annotation in
	// OpenShift namespaces.
	uidRangeRegexp = regexp.MustCompile(`^(\d+)/\d+`)
	// GSMproject is the name of the GCP Secret Manager project where the secrets are stored.
	GSMproject = "openshift-ci-secrets"
)

func (s *multiStageTestStep) createSharedDirSecret(ctx context.Context) error {
	logrus.Debugf("Creating multi-stage test shared directory %q", s.name)
	secret := &coreapi.Secret{ObjectMeta: meta.ObjectMeta{
		Namespace: s.jobSpec.Namespace(),
		Name:      s.name,
		Labels:    map[string]string{api.SkipCensoringLabel: "true"},
	}}
	if err := s.client.Delete(ctx, secret); err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("cannot delete shared directory %q: %w", s.name, err)
	}
	return s.client.Create(ctx, secret)
}

// createSPCs creates all the SecretProviderClasses (SPCs) needed
// to fetch the appropriate secrets from GCP. This is done before
// the individual steps are run to make sure the appropriate
// SecretProviderClasses already exist and are available for the test pods.
func (s *multiStageTestStep) createSPCs(ctx context.Context) error {
	toCreate := map[string]*csiapi.SecretProviderClass{}

	for _, step := range append(s.pre, append(s.test, s.post...)...) {
		for _, credential := range step.Credentials {
			name := fmt.Sprintf("%s-%s-spc", s.jobSpec.Namespace(), credential.Name)
			if _, exists := toCreate[name]; exists {
				continue
			}
			toCreate[name] = &csiapi.SecretProviderClass{
				TypeMeta: meta.TypeMeta{
					Kind:       "SecretProviderClass",
					APIVersion: csiapi.GroupVersion.String(),
				},
				ObjectMeta: meta.ObjectMeta{
					Name:      name,
					Namespace: s.jobSpec.Namespace(),
				},
				Spec: csiapi.SecretProviderClassSpec{
					Provider: "gcp",
					Parameters: map[string]string{
						"auth":    "provider-adc",
						"secrets": formatSecretsParam(credential.Name),
					},
				},
			}
		}
	}

	for name := range toCreate {
		if err := s.client.Create(ctx, toCreate[name]); err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create SecretProviderClass object for secret: %w", err)
		}
	}
	return nil
}

func formatSecretsParam(name string) string {
	return fmt.Sprintf("- resourceName: \"projects/%s/secrets/%s/versions/latest\"\n  fileName: \"%s\"", GSMproject, name, name)
}

func (s *multiStageTestStep) createCommandConfigMaps(ctx context.Context) error {
	logrus.Debugf("Creating multi-stage test commands configmap for %q", s.name)
	data := make(map[string]string)
	for _, step := range append(s.pre, append(s.test, s.post...)...) {
		data[step.As] = step.Commands
	}
	name := commandConfigMapForTest(s.name)
	yes := true
	commands := &coreapi.ConfigMap{
		ObjectMeta: meta.ObjectMeta{
			Name:      name,
			Namespace: s.jobSpec.Namespace(),
		},
		Data:      data,
		Immutable: &yes,
	}
	// delete old command configmap if it exists
	if err := s.client.Delete(ctx, commands); err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("could not delete command configmap %s: %w", name, err)
	}
	if err := s.client.Create(ctx, commands); err != nil {
		return fmt.Errorf("could not create command configmap %s: %w", name, err)
	}
	return nil
}

func (s *multiStageTestStep) setupRBAC(ctx context.Context) error {
	labels := map[string]string{MultiStageTestLabel: s.name}
	ns := s.jobSpec.Namespace()
	m := meta.ObjectMeta{Namespace: ns, Name: s.name, Labels: labels}
	sa := &coreapi.ServiceAccount{
		ObjectMeta: m,
		ImagePullSecrets: []coreapi.LocalObjectReference{
			{
				Name: api.RegistryPullCredentialsSecret,
			},
		},
	}
	role := &rbacapi.Role{
		ObjectMeta: m,
		Rules: []rbacapi.PolicyRule{{
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{"rolebindings", "roles"},
			Verbs:     []string{"create", "list"},
		}, {
			APIGroups:     []string{""},
			Resources:     []string{"secrets"},
			ResourceNames: []string{s.name},
			Verbs:         []string{"get", "update"},
		}, {
			APIGroups: []string{"", "image.openshift.io"},
			Resources: []string{"imagestreams/layers"},
			Verbs:     []string{"get"},
		}},
	}
	subj := []rbacapi.Subject{{Kind: "ServiceAccount", Name: s.name}}
	bindings := []rbacapi.RoleBinding{
		{
			ObjectMeta: m,
			RoleRef:    rbacapi.RoleRef{Kind: "Role", Name: s.name},
			Subjects:   subj,
		},
		{
			ObjectMeta: meta.ObjectMeta{
				Namespace: ns,
				Name:      s.name + "-view",
				Labels:    labels,
			},
			RoleRef:  rbacapi.RoleRef{Kind: "ClusterRole", Name: "view"},
			Subjects: subj,
		},
	}
	if s.vpnConf != nil {
		bindings = append(bindings, rbacapi.RoleBinding{
			ObjectMeta: meta.ObjectMeta{Namespace: ns, Name: s.name + "-vpn"},
			RoleRef: rbacapi.RoleRef{
				Kind: "ClusterRole",
				Name: "ci-operator-vpn",
			},
			Subjects: subj,
		})
	}
	if err := util.CreateRBACs(ctx, sa, role, bindings, s.client, 1*time.Second, 1*time.Minute); err != nil {
		return err
	}

	return nil
}

// getNamespaceUID retrieves the base UID configured for the test namespace.
// This is required to restrict unprivileged containers to use that UID when an
// SCC with the `RunAsUser` field set to RunAsAny` is used, as that applies to
// every container in the pod.  There does not seem to be a mechanism in
// OpenShift to do this automatically.
func getNamespaceUID(
	ctx context.Context,
	name string,
	client kubernetes.PodClient,
) (int64, error) {
	var ns coreapi.Namespace
	key := ctrlruntimeclient.ObjectKey{Name: name}
	if err := client.Get(ctx, key, &ns); err != nil {
		return 0, fmt.Errorf("failed to get test namespace: %w", err)
	}
	var uidRange string
	if ns.ObjectMeta.Annotations != nil {
		uidRange = ns.ObjectMeta.Annotations["openshift.io/sa.scc.uid-range"]
	}
	return parseNamespaceUID(uidRange)
}

// parseNamespaceUID extracts the base UID from a `${base}/${size}` range.
func parseNamespaceUID(uidRange string) (int64, error) {
	matches := uidRangeRegexp.FindStringSubmatch(uidRange)
	if matches == nil {
		return 0, fmt.Errorf("invalid namespace UID range: %s", uidRange)
	}
	ret, err := strconv.ParseInt(matches[1], 10, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to parse UID range %q: %w", uidRange, err)
	} else if ret == 0 {
		return 0, fmt.Errorf("invalid namespace UID range: %s", uidRange)
	}
	return ret, nil
}
