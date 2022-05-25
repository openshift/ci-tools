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

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/util"
)

var (
	// uidRangeRegexp parses the base UID from a `${base}/${size}` UID range.
	// This is the format of the `openshift.io/sa.scc.uid-range` annotation in
	// OpenShift namespaces.
	uidRangeRegexp = regexp.MustCompile(`^(\d+)/\d+`)
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

func (s *multiStageTestStep) createCredentials(ctx context.Context) error {
	logrus.Debugf("Creating multi-stage test credentials for %q", s.name)
	toCreate := map[string]*coreapi.Secret{}
	for _, step := range append(s.pre, append(s.test, s.post...)...) {
		for _, credential := range step.Credentials {
			// we don't want secrets imported from separate namespaces to collide
			// but we want to keep them generally recognizable for debugging, and the
			// chance we get a second-level collision (ns-a, name) and (ns, a-name) is
			// small, so we can get away with this string prefixing
			name := fmt.Sprintf("%s-%s", credential.Namespace, credential.Name)
			if _, ok := toCreate[name]; ok {
				continue
			}
			raw := &coreapi.Secret{}
			if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: credential.Namespace, Name: credential.Name}, raw); err != nil {
				return fmt.Errorf("could not read source credential: %w", err)
			}
			toCreate[name] = &coreapi.Secret{
				TypeMeta: raw.TypeMeta,
				ObjectMeta: meta.ObjectMeta{
					Name:      name,
					Namespace: s.jobSpec.Namespace(),
				},
				Type:       raw.Type,
				Data:       raw.Data,
				StringData: raw.StringData,
			}
		}
	}

	for name := range toCreate {
		if err := s.client.Create(ctx, toCreate[name]); err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create source credential: %w", err)
		}
	}
	return nil
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
	sa := &coreapi.ServiceAccount{ObjectMeta: m}
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
			ObjectMeta: meta.ObjectMeta{Namespace: ns, Name: "test-runner-view-binding", Labels: labels},
			RoleRef:    rbacapi.RoleRef{Kind: "ClusterRole", Name: "view"},
			Subjects:   subj,
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
