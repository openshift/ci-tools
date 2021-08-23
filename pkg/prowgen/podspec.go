package prowgen

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

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
	meta        cioperatorapi.Metadata
	buildErrors []error

	needsBoskos     bool
	needsPullSecret bool

	claims                           bool
	promotion                        bool
	ghToken                          bool
	reuseGitHubTokenDecorationVolume bool

	releaseRpmsVersion string
	clusterProfile     cioperatorapi.ClusterProfile
	template           string
	templateCommand    string
	templateTestIST    string

	targets sets.String
	secrets []cioperatorapi.Secret
}

// CiOperatorPodSpecGenerator is a builder-pattern for building PodSpecs that
// run ci-operator for various purposes, ensuring the consistency of volumes,
// mounts, parameters and other content.
type CiOperatorPodSpecGenerator interface {
	CIPullSecret() CiOperatorPodSpecGenerator
	Claims() CiOperatorPodSpecGenerator
	ClusterProfile(profile cioperatorapi.ClusterProfile) CiOperatorPodSpecGenerator
	GitHubToken(needsVolume bool) CiOperatorPodSpecGenerator
	LeaseClient() CiOperatorPodSpecGenerator
	Promotion() CiOperatorPodSpecGenerator
	ReleaseRpms(version string) CiOperatorPodSpecGenerator
	Secrets(secrets ...*cioperatorapi.Secret) CiOperatorPodSpecGenerator
	Targets(targets ...string) CiOperatorPodSpecGenerator
	Template(template, command, fromImage string) CiOperatorPodSpecGenerator

	Build() (*corev1.PodSpec, error)
}

// NewCiOperatorPodSpecGenerator returns a new CiOperatorPodSpecGenerator instance
// that builds `PodSpec` instances based on the provided configuration, in a
// consistent way. Without any further configuration, the PodSpec will be a
// default one. If the passed `info` instance instructs that the job should be
// generated private, the `PodSpec` will be generated to expose the necessary
// secrets for ci-operator to be able to interact with the private repository.
func NewCiOperatorPodSpecGenerator(meta cioperatorapi.Metadata) CiOperatorPodSpecGenerator {
	return &ciOperatorPodSpecGenerator{
		meta:    meta,
		targets: sets.NewString(),
	}
}

// ReleaseRpms adds environment variables that expose OCP release RPMs needed by
// several template-based tests. Does not have any effect when the generator
// is not configured to produce a template test (see `Template`)
func (c *ciOperatorPodSpecGenerator) ReleaseRpms(version string) CiOperatorPodSpecGenerator {
	c.releaseRpmsVersion = version
	return c
}

// CIPullSecret exposes a shared CI pull secret via a mounted volume and a `--secret-dir`
// option passed to ci-operator
func (c *ciOperatorPodSpecGenerator) CIPullSecret() CiOperatorPodSpecGenerator {
	c.needsPullSecret = true
	return c
}

// Secrets exposes the configured secrets via mounted volumes and a `--secret-dir`
// option passed to ci-operator
func (c *ciOperatorPodSpecGenerator) Secrets(secrets ...*cioperatorapi.Secret) CiOperatorPodSpecGenerator {
	for i := range secrets {
		if secrets[i] != nil {
			c.secrets = append(c.secrets, *secrets[i])
		}
	}
	return c
}

// Promotion configures the PodSpec to run ci-operator in a promoting mode,
// supplying the necessary secrets and ci-operator parameter
func (c *ciOperatorPodSpecGenerator) Promotion() CiOperatorPodSpecGenerator {
	c.promotion = true
	return c
}

// ClusterProfile exposes the configured cluster profile to ci-operator via a
// mounted volume
func (c *ciOperatorPodSpecGenerator) ClusterProfile(profile cioperatorapi.ClusterProfile) CiOperatorPodSpecGenerator {
	c.clusterProfile = profile
	return c
}

// Template exposes the configured template to ci-operator via a mounted volume,
// and configures the environment variables consumed by the templates
// fromImage can be empty. If it is not empty, it configures an environmental
// variable holding the test image ImageStreamTag (only used by the custom image
// test template)
func (c *ciOperatorPodSpecGenerator) Template(template, command, fromImage string) CiOperatorPodSpecGenerator {
	c.template = template
	c.templateCommand = command
	c.templateTestIST = fromImage
	return c
}

// LeaseClient configures ci-operator to be able to interact with Boskos (lease
// server), providing the necessary secrets to do so
func (c *ciOperatorPodSpecGenerator) LeaseClient() CiOperatorPodSpecGenerator {
	c.needsBoskos = true
	return c
}

// Claims configures ci-operator to be able to interact with the Hive cluster
// for tests that use Claims, providing the necessary secrets to do so
func (c *ciOperatorPodSpecGenerator) Claims() CiOperatorPodSpecGenerator {
	c.claims = true
	return c.CIPullSecret()
}

// Targets configures ci-operator to build specified targets
func (c *ciOperatorPodSpecGenerator) Targets(targets ...string) CiOperatorPodSpecGenerator {
	for i := range targets {
		c.targets.Insert(targets[i])
	}
	return c
}

// GitHubToken configures ci-operator to use a GH token to authenticate to GitHub
// (to be able to get source code from GitHub repositories). The necessary secret
// is made available in a volume that may be provided by Prow already: in this case
// that volume must be reused instead of added to the PodSpec.
func (c *ciOperatorPodSpecGenerator) GitHubToken(reuseDecorationVolume bool) CiOperatorPodSpecGenerator {
	c.ghToken = true
	c.reuseGitHubTokenDecorationVolume = reuseDecorationVolume
	return c

}

func (c *ciOperatorPodSpecGenerator) handleGitHubToken(spec *corev1.PodSpec, container *corev1.Container) {
	if !c.ghToken {
		return
	}

	if !c.reuseGitHubTokenDecorationVolume {
		spec.Volumes = append(spec.Volumes, corev1.Volume{
			Name: cioperatorapi.OauthTokenSecretName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: cioperatorapi.OauthTokenSecretName},
			},
		})
	}

	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      cioperatorapi.OauthTokenSecretName,
		MountPath: oauthTokenPath,
		ReadOnly:  true,
	})

	container.Args = append(container.Args, fmt.Sprintf("--oauth-token-path=%s", filepath.Join(oauthTokenPath, oauthKey)))
}

func (c *ciOperatorPodSpecGenerator) handleClaims(spec *corev1.PodSpec, container *corev1.Container) {
	if !c.claims {
		return
	}

	container.Args = append(container.Args, cioperatorapi.HiveControlPlaneKubeconfigSecretArg)
	spec.Volumes = append(spec.Volumes, corev1.Volume{
		Name: cioperatorapi.HiveControlPlaneKubeconfigSecret,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: cioperatorapi.HiveControlPlaneKubeconfigSecret},
		},
	})
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      cioperatorapi.HiveControlPlaneKubeconfigSecret,
		MountPath: fmt.Sprintf("/secrets/%s", cioperatorapi.HiveControlPlaneKubeconfigSecret),
		ReadOnly:  true,
	})
}

func (c *ciOperatorPodSpecGenerator) handleSecrets(spec *corev1.PodSpec, container *corev1.Container) {
	if c.needsPullSecret {
		// If the ci-operator configuration resolves an official release,
		// we need to create a pull secret in the namespace that ci-operator
		// runs in. While the --secret-dir mechanism is *meant* to provide
		// secrets to the tests themselves, this secret will have no consumer
		// and that is OK. We just need it to exist in the test namespace so
		// that the image import controller can use it.
		c.secrets = append(c.secrets, cioperatorapi.Secret{Name: "ci-pull-credentials"})
	}

	for _, secret := range c.secrets {
		name := strings.ReplaceAll(secret.Name, ".", "-")

		spec.Volumes = append(spec.Volumes, corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: secret.Name},
			},
		})
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      name,
			MountPath: fmt.Sprintf("/secrets/%s", secret.Name),
			ReadOnly:  true,
		})
		container.Args = append(container.Args, fmt.Sprintf("--secret-dir=/secrets/%s", secret.Name))
	}
}

func (c *ciOperatorPodSpecGenerator) handleTemplate(spec *corev1.PodSpec, container *corev1.Container) {
	if c.template == "" {
		if c.templateTestIST != "" {
			c.buildErrors = append(c.buildErrors, errors.New("empty template but nonempty templateTestIST"))
		}
		if c.releaseRpmsVersion != "" {
			c.buildErrors = append(c.buildErrors, errors.New("empty template but nonempty release RPM version"))
		}
		return
	}
	if c.clusterProfile == "" {
		c.buildErrors = append(c.buildErrors, errors.New("template requires cluster profile"))
		return
	}
	if len(c.targets) != 1 {
		// This is fairly arbitrary but the target name must match the mount path, which we can only assume with one target
		c.buildErrors = append(c.buildErrors, errors.New("ci-operator must have exactly one target when using a template"))
		return
	}

	target := c.targets.List()[0]
	templatePath := fmt.Sprintf("/usr/local/%s", target)
	spec.Volumes = append(spec.Volumes, generateConfigMapVolume("job-definition", []string{fmt.Sprintf("prow-job-%s", c.template)}))
	container.Args = append(container.Args, fmt.Sprintf("--template=%s", templatePath))
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: "job-definition", MountPath: templatePath, SubPath: fmt.Sprintf("%s.yaml", c.template)})
	container.Env = append(
		container.Env,
		corev1.EnvVar{Name: "CLUSTER_TYPE", Value: c.clusterProfile.ClusterType()},
		corev1.EnvVar{Name: "JOB_NAME_SAFE", Value: strings.Replace(target, "_", "-", -1)},
		corev1.EnvVar{Name: "TEST_COMMAND", Value: c.templateCommand},
	)
	if c.templateTestIST != "" {
		container.Env = append(container.Env, corev1.EnvVar{Name: "TEST_IMAGESTREAM_TAG", Value: c.templateTestIST})
	}
	if c.releaseRpmsVersion != "" && (c.meta.Org != "openshift" || c.meta.Repo != "origin") {
		url := cioperatorapi.URLForService(cioperatorapi.ServiceRPMs)
		var repoPath = fmt.Sprintf("%s/openshift-origin-v%s/", url, c.releaseRpmsVersion)
		if strings.HasPrefix(c.releaseRpmsVersion, "origin-v") {
			repoPath = fmt.Sprintf("%s/openshift-%s/", url, c.releaseRpmsVersion)
		}
		container.Env = append(container.Env, corev1.EnvVar{
			Name:  "RPM_REPO_OPENSHIFT_ORIGIN",
			Value: repoPath,
		})
	}
}

func (c *ciOperatorPodSpecGenerator) handleLeaseClient(spec *corev1.PodSpec, container *corev1.Container) {
	if !c.needsBoskos {
		return
	}
	container.Args = append(container.Args, "--lease-server-credentials-file=/etc/boskos/credentials")
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      "boskos",
		MountPath: "/etc/boskos",
		ReadOnly:  true,
	})
	spec.Volumes = append(spec.Volumes, corev1.Volume{
		Name: "boskos",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: "boskos-credentials",
				Items:      []corev1.KeyToPath{{Key: "credentials", Path: "credentials"}},
			},
		},
	})
}

func (c *ciOperatorPodSpecGenerator) handlePromotion(spec *corev1.PodSpec, container *corev1.Container) {
	if !c.promotion {
		return
	}
	container.Args = append(container.Args,
		"--promote",
		fmt.Sprintf("--image-mirror-push-secret=%s", filepath.Join(cioperatorapi.RegistryPushCredentialsCICentralSecretMountPath, corev1.DockerConfigJsonKey)))
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      "push-secret",
		MountPath: cioperatorapi.RegistryPushCredentialsCICentralSecretMountPath,
		ReadOnly:  true,
	})
	spec.Volumes = append(spec.Volumes, corev1.Volume{
		Name: "push-secret",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: cioperatorapi.RegistryPushCredentialsCICentralSecret},
		},
	})
}

func (c *ciOperatorPodSpecGenerator) handleTargets(container *corev1.Container) {
	var args []string
	for target := range c.targets {
		args = append(args, fmt.Sprintf("--target=%s", target))
	}
	sort.Strings(args)
	container.Args = append(container.Args, args...)
}

func (c *ciOperatorPodSpecGenerator) handleVariant(container *corev1.Container) {
	if len(c.meta.Variant) > 0 {
		container.Args = append(container.Args, fmt.Sprintf("--variant=%s", c.meta.Variant))
	}
}

func (c *ciOperatorPodSpecGenerator) handleClusterProfile(spec *corev1.PodSpec, container *corev1.Container) {
	if c.clusterProfile == "" {
		return
	}
	if len(c.targets) != 1 {
		// This is fairly arbitrary but the target name must match the mount path, which we can only assume with one target
		c.buildErrors = append(c.buildErrors, errors.New("ci-operator must have exactly one target when using a cluster profile"))
		return
	}
	target := c.targets.List()[0]
	spec.Volumes = append(spec.Volumes, generateClusterProfileVolume(c.clusterProfile, c.clusterProfile.ClusterType()))
	clusterProfilePath := fmt.Sprintf("/usr/local/%s-cluster-profile", target)
	container.Args = append(container.Args, fmt.Sprintf("--secret-dir=%s", clusterProfilePath))
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: "cluster-profile", MountPath: clusterProfilePath})
}

// Build produces and returns a new `PodSpec` containing all configured elements
func (c *ciOperatorPodSpecGenerator) Build() (*corev1.PodSpec, error) {
	spec := defaultPodSpec.DeepCopy()
	container := &spec.Containers[0]

	c.handleGitHubToken(spec, container)
	c.handleClaims(spec, container)
	c.handleTargets(container)
	c.handleVariant(container)
	c.handleSecrets(spec, container)
	c.handleClusterProfile(spec, container)
	c.handleTemplate(spec, container)
	c.handleLeaseClient(spec, container)
	c.handlePromotion(spec, container)

	return spec, kerrors.NewAggregate(c.buildErrors)
}
