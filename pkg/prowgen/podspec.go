package prowgen

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	kerrors "k8s.io/apimachinery/pkg/util/errors"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
)

var defaultPodSpec = corev1.PodSpec{
	ServiceAccountName: "ci-operator",
	Containers: []corev1.Container{
		{
			Args: []string{
				"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
				"--gcs-upload-secret=/secrets/gcs/service-account.json",
				"--report-credentials-file=/etc/report/credentials",
			},
			Command:         []string{"ci-operator"},
			Image:           "ci-operator:latest",
			ImagePullPolicy: corev1.PullAlways,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "pull-secret",
					MountPath: "/etc/pull-secret",
					ReadOnly:  true,
				},
				{
					Name:      "result-aggregator",
					MountPath: "/etc/report",
					ReadOnly:  true,
				},
				{
					Name:      "gcs-credentials",
					MountPath: cioperatorapi.GCSUploadCredentialsSecretMountPath,
					ReadOnly:  true,
				},
			},
		},
	},
	Volumes: []corev1.Volume{
		{
			Name: "pull-secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: "registry-pull-credentials"},
			},
		},
		{
			Name: "result-aggregator",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
			},
		},
	},
}

type ciOperatorPodSpecGenerator struct {
	mutators    []PodSpecMutator
	buildErrors []error
}

// PodSpecMutator is a mutation function operating over a PodSpec pointer.
// The changes performed on the PodSpec must not depend on any previous
// state of the PodSpec, and must resolve any conflicts it encounters.
// The mutator function must also correctly handle being called multiple calls
type PodSpecMutator func(spec *corev1.PodSpec) error

// CiOperatorPodSpecGenerator is a builder-pattern for building PodSpecs that
// run ci-operator for various purposes, ensuring the consistency of volumes,
// mounts, parameters and other content.
type CiOperatorPodSpecGenerator interface {
	// Add adds one or more mutations to be performed to build the final PodSpec
	Add(mutators ...PodSpecMutator) CiOperatorPodSpecGenerator
	// Build returns the PodSpec built by taking the default PodSpec, applying
	// all added mutators and sorting several list fields by their keys.
	Build() (*corev1.PodSpec, error)
	// MustBuild is same as Build but panics whenever an error would be reported
	MustBuild() *corev1.PodSpec
}

// NewCiOperatorPodSpecGenerator returns a new CiOperatorPodSpecGenerator instance
func NewCiOperatorPodSpecGenerator() CiOperatorPodSpecGenerator {
	return &ciOperatorPodSpecGenerator{}
}

func aggregateMutator(mutators ...PodSpecMutator) PodSpecMutator {
	return func(spec *corev1.PodSpec) error {
		var errs []error
		for _, f := range mutators {
			errs = append(errs, f(spec))
		}
		return kerrors.NewAggregate(errs)
	}
}

func (c *ciOperatorPodSpecGenerator) Add(f ...PodSpecMutator) CiOperatorPodSpecGenerator {
	c.mutators = append(c.mutators, f...)
	return c
}

func addEnvVar(container *corev1.Container, want corev1.EnvVar) error {
	var found bool
	for i := range container.Env {
		if container.Env[i].Name != want.Name {
			continue
		}
		if !reflect.DeepEqual(want, container.Env[i]) {
			return fmt.Errorf("environment variable '%s' added with different value", want.Name)
		}
		found = true
	}
	if !found {
		container.Env = append(container.Env, want)
	}
	return nil
}

func addUniqueParameter(container *corev1.Container, wantArg string) {
	for i := range container.Args {
		if container.Args[i] == wantArg {
			return
		}
	}
	container.Args = append(container.Args, wantArg)
}

func addVolume(spec *corev1.PodSpec, wantVolume corev1.Volume) error {
	var foundVolume bool
	for i := range spec.Volumes {
		if spec.Volumes[i].Name != wantVolume.Name {
			continue
		}
		if !reflect.DeepEqual(wantVolume.VolumeSource, spec.Volumes[i].VolumeSource) {
			return fmt.Errorf("volume '%s' added with different sources", wantVolume.Name)
		}
		foundVolume = true
	}
	if !foundVolume {
		spec.Volumes = append(spec.Volumes, wantVolume)
	}

	return nil
}

func addVolumeMount(container *corev1.Container, wantMount corev1.VolumeMount) error {
	var foundMount bool
	for i := range container.VolumeMounts {
		if container.VolumeMounts[i].MountPath != wantMount.MountPath {
			continue
		}
		if !reflect.DeepEqual(wantMount, container.VolumeMounts[i]) {
			return fmt.Errorf("multiple different volumes mounted to '%s'", wantMount.MountPath)
		}
		foundMount = true
	}
	if !foundMount {
		container.VolumeMounts = append(container.VolumeMounts, wantMount)
	}
	return nil
}

func makeSecretAddingMutator(secretName string) PodSpecMutator {
	return func(spec *corev1.PodSpec) error {
		safeName := strings.ReplaceAll(secretName, ".", "-")

		wantVolume := corev1.Volume{
			Name: safeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: secretName},
			},
		}
		if err := addVolume(spec, wantVolume); err != nil {
			return err
		}

		container := &spec.Containers[0]
		wantMount := corev1.VolumeMount{
			Name:      safeName,
			MountPath: fmt.Sprintf("/secrets/%s", secretName),
			ReadOnly:  true,
		}
		if err := addVolumeMount(container, wantMount); err != nil {
			return err
		}

		addUniqueParameter(container, fmt.Sprintf("--secret-dir=/secrets/%s", secretName))
		return nil
	}
}

const (
	envRepoPath = "RPM_REPO_OPENSHIFT_ORIGIN"
)

// ReleaseRpms adds environment variables that expose OCP release RPMs needed by
// several template-based tests. Does not have any effect when the generator
// is not configured to produce a template test (see `Template`)
func ReleaseRpms(version string, meta cioperatorapi.Metadata) PodSpecMutator {
	return func(spec *corev1.PodSpec) error {
		if meta.Org == "openshift" && meta.Repo == "origin" {
			return nil
		}
		container := &spec.Containers[0]
		url := cioperatorapi.URLForService(cioperatorapi.ServiceRPMs)
		var repoPath = fmt.Sprintf("%s/openshift-origin-v%s/", url, version)
		if strings.HasPrefix(version, "origin-v") {
			repoPath = fmt.Sprintf("%s/openshift-%s/", url, version)
		}
		return addEnvVar(container, corev1.EnvVar{
			Name:  envRepoPath,
			Value: repoPath,
		})
	}
}

// CIPullSecret exposes a shared CI pull secret via a mounted volume and a `--secret-dir`
// option passed to ci-operator
func CIPullSecret() PodSpecMutator {
	// If the ci-operator configuration resolves an official release,
	// we need to create a pull secret in the namespace that ci-operator
	// runs in. While the --secret-dir mechanism is *meant* to provide
	// secrets to the tests themselves, this secret will have no consumer
	// and that is OK. We just need it to exist in the test namespace so
	// that the image import controller can use it.
	return makeSecretAddingMutator("ci-pull-credentials")
}

// Secrets exposes the configured secrets via mounted volumes and a `--secret-dir`
// option passed to ci-operator
func Secrets(secrets ...*cioperatorapi.Secret) PodSpecMutator {
	var mutators []PodSpecMutator
	for i := range secrets {
		if secrets[i] != nil {
			mutators = append(mutators, makeSecretAddingMutator(secrets[i].Name))
		}
	}
	return aggregateMutator(mutators...)
}

const (
	pushSecretVolumeName = "push-secret"
	promoteParam         = "--promote"
)

var (
	pushSecretParam  = fmt.Sprintf("--image-mirror-push-secret=%s", filepath.Join(cioperatorapi.RegistryPushCredentialsCICentralSecretMountPath, corev1.DockerConfigJsonKey))
	pushSecretVolume = corev1.Volume{
		Name: pushSecretVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: cioperatorapi.RegistryPushCredentialsCICentralSecret},
		},
	}

	pushSecretVolumeMount = corev1.VolumeMount{
		Name:      pushSecretVolumeName,
		MountPath: cioperatorapi.RegistryPushCredentialsCICentralSecretMountPath,
		ReadOnly:  true,
	}
)

// Promotion configures the PodSpec to run ci-operator in a promoting mode,
// supplying the necessary secrets and the ci-operator parameter
func Promotion() PodSpecMutator {
	return func(spec *corev1.PodSpec) error {
		container := &spec.Containers[0]
		if err := addVolume(spec, pushSecretVolume); err != nil {
			return err
		}
		if err := addVolumeMount(container, pushSecretVolumeMount); err != nil {
			return err
		}
		addUniqueParameter(container, promoteParam)
		addUniqueParameter(container, pushSecretParam)
		return nil
	}
}

const (
	clusterProfileVolume = "cluster-profile"
)

func generateClusterProfileVolume(profile cioperatorapi.ClusterProfile) corev1.Volume {
	if secret, cm := profile.Secret(), profile.ConfigMap(); cm == "" {
		return corev1.Volume{
			Name: clusterProfileVolume,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: secret},
			},
		}
	} else {
		return corev1.Volume{
			Name: clusterProfileVolume,
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{{
						Secret: &corev1.SecretProjection{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: secret,
							},
						},
					}, {
						ConfigMap: &corev1.ConfigMapProjection{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: cm,
							},
						},
					}},
				},
			},
		}
	}
}

func generateConfigMapVolume(name string, templates []string) corev1.Volume {
	ret := corev1.Volume{Name: name}
	switch len(templates) {
	case 0:
	case 1:
		ret.VolumeSource = corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: templates[0],
				},
			},
		}
	default:
		ret.VolumeSource = corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{},
		}
		s := &ret.VolumeSource.Projected.Sources
		for _, t := range templates {
			*s = append(*s, corev1.VolumeProjection{
				ConfigMap: &corev1.ConfigMapProjection{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: t,
					},
				},
			})
		}
	}
	return ret
}

// ClusterProfile exposes the configured cluster profile to ci-operator via a
// mounted volume
func ClusterProfile(profile cioperatorapi.ClusterProfile, target string) PodSpecMutator {
	return func(spec *corev1.PodSpec) error {
		container := &spec.Containers[0]
		wantVolume := generateClusterProfileVolume(profile)
		if err := addVolume(spec, wantVolume); err != nil {
			return err
		}
		clusterProfilePath := fmt.Sprintf("/usr/local/%s-cluster-profile", target)
		wantVolumeMount := corev1.VolumeMount{Name: clusterProfileVolume, MountPath: clusterProfilePath}

		if err := addVolumeMount(container, wantVolumeMount); err != nil {
			return nil
		}
		addUniqueParameter(container, fmt.Sprintf("--secret-dir=%s", clusterProfilePath))
		return nil
	}
}

const (
	envSafeJobName     = "JOB_NAME_SAFE"
	envTestCommand     = "TEST_COMMAND"
	envClusterType     = "CLUSTER_TYPE"
	envCustomTestImage = "TEST_IMAGESTREAM_TAG"

	templateVolume = "job-definition"
)

// Template exposes the configured template to ci-operator via a mounted volume,
// and configures the environment variables consumed by the templates
// fromImage can be empty. If it is not empty, it configures an environmental
// variable holding the test image ImageStreamTag (only used by the custom image
// test template)
// Template() also implies and includes a corresponding ClusterProfile() mutator.
func Template(template, command, fromImage, target string, profile cioperatorapi.ClusterProfile) PodSpecMutator {
	ensureTemplate := func(spec *corev1.PodSpec) error {
		container := &spec.Containers[0]
		wantVolume := generateConfigMapVolume(templateVolume, []string{fmt.Sprintf("prow-job-%s", template)})
		if err := addVolume(spec, wantVolume); err != nil {
			return err
		}

		templatePath := fmt.Sprintf("/usr/local/%s", target)
		wantMount := corev1.VolumeMount{Name: templateVolume, MountPath: templatePath, SubPath: fmt.Sprintf("%s.yaml", template)}
		if err := addVolumeMount(container, wantMount); err != nil {
			return err
		}

		addUniqueParameter(container, fmt.Sprintf("--template=%s", templatePath))

		defaultEnvVars := []corev1.EnvVar{
			{Name: envSafeJobName, Value: strings.Replace(target, "_", "-", -1)},
			{Name: envTestCommand, Value: command},
			{Name: envClusterType, Value: profile.ClusterType()},
		}
		for i := range defaultEnvVars {
			if err := addEnvVar(container, defaultEnvVars[i]); err != nil {
				return err
			}
		}

		if fromImage != "" {
			if err := addEnvVar(container, corev1.EnvVar{Name: envCustomTestImage, Value: fromImage}); err != nil {
				return err
			}
		}

		return nil
	}
	return aggregateMutator(
		ensureTemplate,
		ClusterProfile(profile, target),
	)
}

const (
	boskosVolumeName           = "boskos"
	boskosCredentialsParameter = "--lease-server-credentials-file=/etc/boskos/credentials"
)

var (
	boskosVolume = corev1.Volume{
		Name: boskosVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: "boskos-credentials",
				Items:      []corev1.KeyToPath{{Key: "credentials", Path: "credentials"}},
			},
		},
	}
	boskosVolumeMount = corev1.VolumeMount{
		Name:      boskosVolumeName,
		MountPath: "/etc/boskos",
		ReadOnly:  true,
	}
)

// LeaseClient configures ci-operator to be able to interact with Boskos (lease
// server), providing the necessary secrets to do so
func LeaseClient() PodSpecMutator {
	return func(spec *corev1.PodSpec) error {
		container := &spec.Containers[0]
		if err := addVolume(spec, boskosVolume); err != nil {
			return err
		}
		if err := addVolumeMount(container, boskosVolumeMount); err != nil {
			return err
		}
		addUniqueParameter(container, boskosCredentialsParameter)
		return nil
	}

}

var (
	hiveSecretVolume = corev1.Volume{
		Name: cioperatorapi.HiveControlPlaneKubeconfigSecret,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: cioperatorapi.HiveControlPlaneKubeconfigSecret},
		},
	}
	hiveSecretVolumeMount = corev1.VolumeMount{
		Name:      cioperatorapi.HiveControlPlaneKubeconfigSecret,
		MountPath: fmt.Sprintf("/secrets/%s", cioperatorapi.HiveControlPlaneKubeconfigSecret),
		ReadOnly:  true,
	}
)

// Claims configures ci-operator to be able to interact with the Hive cluster
// for tests that use Claims, providing the necessary secrets to do so
func Claims() PodSpecMutator {
	addClaims := func(spec *corev1.PodSpec) error {
		container := &spec.Containers[0]
		if err := addVolume(spec, hiveSecretVolume); err != nil {
			return err
		}
		if err := addVolumeMount(container, hiveSecretVolumeMount); err != nil {
			return err
		}
		addUniqueParameter(container, cioperatorapi.HiveControlPlaneKubeconfigSecretArg)

		return nil
	}

	return aggregateMutator(addClaims, CIPullSecret())
}

// Targets configures ci-operator to build specified targets
func Targets(targets ...string) PodSpecMutator {
	return func(spec *corev1.PodSpec) error {
		container := &spec.Containers[0]
		for _, target := range targets {
			addUniqueParameter(container, fmt.Sprintf("--target=%s", target))
		}
		return nil
	}
}

var (
	githubTokenVolume = corev1.Volume{
		Name: cioperatorapi.OauthTokenSecretName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: cioperatorapi.OauthTokenSecretName},
		},
	}
	githubTokenVolumeMount = corev1.VolumeMount{
		Name:      cioperatorapi.OauthTokenSecretName,
		MountPath: oauthTokenPath,
		ReadOnly:  true,
	}
	githubTokenParameter = fmt.Sprintf("--oauth-token-path=%s", filepath.Join(oauthTokenPath, oauthKey))
)

// GitHubToken configures ci-operator to use a GH token to authenticate to GitHub
// (to be able to get source code from GitHub repositories). The necessary secret
// is made available in a volume that may be provided by Prow already: in this case
// that volume must be reused instead of added to the PodSpec.
func GitHubToken(reuseDecorationVolume bool) PodSpecMutator {
	return func(spec *corev1.PodSpec) error {
		container := &spec.Containers[0]
		if !reuseDecorationVolume {
			if err := addVolume(spec, githubTokenVolume); err != nil {
				return nil
			}
		}
		if err := addVolumeMount(container, githubTokenVolumeMount); err != nil {
			return nil
		}
		addUniqueParameter(container, githubTokenParameter)
		return nil
	}
}

func Variant(variant string) PodSpecMutator {
	return func(spec *corev1.PodSpec) error {
		if len(variant) > 0 {
			container := &spec.Containers[0]
			addUniqueParameter(container, fmt.Sprintf("--variant=%s", variant))
		}
		return nil
	}
}

func CustomHashInput(input string) PodSpecMutator {
	return func(spec *corev1.PodSpec) error {
		container := &spec.Containers[0]
		addUniqueParameter(container, fmt.Sprintf("--input-hash=%s", input))
		return nil
	}
}

// InjectTestFrom configures ci-operator to inject the specified test from the
// specified ci-operator config into the base config and target it
func InjectTestFrom(source *cioperatorapi.MetadataWithTest) PodSpecMutator {
	addInjectParams := func(spec *corev1.PodSpec) error {
		container := &spec.Containers[0]
		var variant string
		if source.Variant != "" {
			variant = fmt.Sprintf("__%s", source.Variant)
		}
		for name, item := range map[string]string{
			"organization": source.Org,
			"repository":   source.Repo,
			"branch":       source.Branch,
			"test":         source.Test,
		} {
			if item == "" {
				return fmt.Errorf("%s cannot be empty in injected test specification", name)
			}
		}
		coordinate := fmt.Sprintf("%s/%s@%s%s:%s", source.Org, source.Repo, source.Branch, variant, source.Test)
		addUniqueParameter(container, fmt.Sprintf("--with-test-from=%s", coordinate))
		return nil
	}
	return aggregateMutator(Targets(source.Test), addInjectParams)
}

// Build produces and returns a new `PodSpec` containing all configured elements
func (c *ciOperatorPodSpecGenerator) Build() (*corev1.PodSpec, error) {
	spec := defaultPodSpec.DeepCopy()
	container := &spec.Containers[0]

	c.buildErrors = append(c.buildErrors, aggregateMutator(c.mutators...)(spec))

	sort.Slice(spec.Volumes, func(i, j int) bool {
		return spec.Volumes[i].Name < spec.Volumes[j].Name
	})
	sort.Slice(container.Env, func(i, j int) bool {
		return container.Env[i].Name < container.Env[j].Name
	})
	sort.Slice(container.VolumeMounts, func(i, j int) bool {
		return container.VolumeMounts[i].Name < container.VolumeMounts[j].Name
	})

	canSortArgs := true
	for i := range container.Args {
		if !strings.HasPrefix(container.Args[i], "--") {
			canSortArgs = false
			break
		}
	}
	if canSortArgs {
		sort.Strings(container.Args)
	}

	return spec, kerrors.NewAggregate(c.buildErrors)
}

// MustBuild produces and returns a new `PodSpec` containing all configured elements
// It panics on building errors.
func (c *ciOperatorPodSpecGenerator) MustBuild() *corev1.PodSpec {
	podSpec, err := c.Build()
	if err != nil {
		panic("BUG: PodSpec generator failed")
	}
	return podSpec
}

// fakePodSpecBuilder is a fake implementation of the CiOperatorPodSpecGenerator interface, useful
// for testing.
type fakePodSpecBuilder int

func (f *fakePodSpecBuilder) Add(_ ...PodSpecMutator) CiOperatorPodSpecGenerator {
	return nil
}
func (f *fakePodSpecBuilder) Build() (*corev1.PodSpec, error) {
	return nil, nil
}
func (f *fakePodSpecBuilder) MustBuild() *corev1.PodSpec {
	return nil
}

func newFakePodSpecBuilder() CiOperatorPodSpecGenerator {
	f := fakePodSpecBuilder(0)
	return &f
}
