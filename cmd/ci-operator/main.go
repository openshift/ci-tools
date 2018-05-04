package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	coreapi "k8s.io/api/core/v1"
	rbacapi "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	rbacclientset "k8s.io/client-go/kubernetes/typed/rbac/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	imageapi "github.com/openshift/api/image/v1"
	projectapi "github.com/openshift/api/project/v1"
	templateapi "github.com/openshift/api/template/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	"github.com/openshift/client-go/project/clientset/versioned"
	templatescheme "github.com/openshift/client-go/template/clientset/versioned/scheme"

	"github.com/openshift/ci-operator/pkg/api"
	"github.com/openshift/ci-operator/pkg/steps"
)

func main() {
	opt := bindOptions()
	flag.Parse()
	opt.templatePaths = flag.Args()

	if err := opt.Validate(); err != nil {
		fmt.Printf("Invalid options: %v\n", err)
		os.Exit(1)
	}

	if err := opt.Complete(); err != nil {
		fmt.Printf("Invalid environment: %v\n", err)
		os.Exit(1)
	}

	if err := opt.Run(); err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
}

type stringSlice struct {
	values []string
}

func (s *stringSlice) String() string {
	return strings.Join(s.values, string(filepath.Separator))
}

func (s *stringSlice) Set(value string) error {
	s.values = append(s.values, value)
	return nil
}

type options struct {
	templatePaths     []string
	secretDirectories stringSlice

	dry         bool
	writeParams string

	namespace           string
	baseNamespace       string
	idleCleanupDuration time.Duration

	inputHash     string
	secrets       []*coreapi.Secret
	templates     []*templateapi.Template
	buildConfig   *api.ReleaseBuildConfiguration
	jobSpec       *steps.JobSpec
	clusterConfig *rest.Config
}

func bindOptions() *options {
	opt := &options{}
	flag.StringVar(&opt.namespace, "namespace", "", "Namespace to create builds into, defaults to build_id from JOB_SPEC")
	flag.StringVar(&opt.baseNamespace, "base-namespace", "stable", "Namespace to read builds from, defaults to stable.")
	flag.Var(&opt.secretDirectories, "secret-dir", "One or more directories that should converted into secrets in the test namespace.")
	flag.BoolVar(&opt.dry, "dry-run", true, "Do not contact the API server.")
	flag.StringVar(&opt.writeParams, "write-params", "", "If set write an env-compatible file with the output of the job.")
	flag.DurationVar(&opt.idleCleanupDuration, "delete-when-idle", opt.idleCleanupDuration, "If no pod is running for longer than this interval, delete the namespace.")
	return opt
}

func (o *options) Validate() error {
	return nil
}

func (o *options) Complete() error {
	configSpec := os.Getenv("CONFIG_SPEC")
	if len(configSpec) == 0 {
		return fmt.Errorf("no CONFIG_SPEC environment variable defined")
	}
	if err := json.Unmarshal([]byte(configSpec), &o.buildConfig); err != nil {
		return fmt.Errorf("malformed build configuration: %v", err)
	}

	jobSpec, err := steps.ResolveSpecFromEnv()
	if err != nil {
		return fmt.Errorf("failed to resolve job spec: %v", err)
	}

	// input hash is unique for a given job definition and input refs
	o.inputHash = inputHash(jobSpec, o.buildConfig)

	if len(o.namespace) == 0 {
		o.namespace = "ci-op-{id}"
	}
	o.namespace = strings.Replace(o.namespace, "{id}", o.inputHash, -1)

	jobSpec.SetNamespace(o.namespace)
	jobSpec.SetBaseNamespace(o.baseNamespace)

	o.jobSpec = jobSpec

	for _, path := range o.secretDirectories.values {
		secret := &coreapi.Secret{Data: make(map[string][]byte)}
		secret.Type = coreapi.SecretTypeOpaque
		secret.Name = filepath.Base(path)
		files, err := ioutil.ReadDir(path)
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			secret.Data[f.Name()], err = ioutil.ReadFile(filepath.Join(path, f.Name()))
			if err != nil {
				return err
			}
		}
		o.secrets = append(o.secrets, secret)
	}

	for _, path := range o.templatePaths {
		contents, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}
		obj, gvk, err := templatescheme.Codecs.UniversalDeserializer().Decode(contents, nil, nil)
		if err != nil {
			return fmt.Errorf("unable to parse template %s: %v", path, err)
		}
		template, ok := obj.(*templateapi.Template)
		if !ok {
			return fmt.Errorf("%s is not a template: %v", path, gvk)
		}
		if len(template.Name) == 0 {
			template.Name = filepath.Base(path)
			template.Name = strings.TrimSuffix(template.Name, filepath.Ext(template.Name))
		}
		o.templates = append(o.templates, template)
	}

	clusterConfig, err := loadClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to load cluster config: %v", err)
	}
	o.clusterConfig = clusterConfig

	return nil
}

// loadClusterConfig loads connection configuration
// for the cluster we're deploying to. We prefer to
// use in-cluster configuration if possible, but will
// fall back to using default rules otherwise.
func loadClusterConfig() (*rest.Config, error) {
	clusterConfig, err := rest.InClusterConfig()
	if err == nil {
		return clusterConfig, nil
	}

	credentials, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return nil, fmt.Errorf("could not load credentials from config: %v", err)
	}

	clusterConfig, err = clientcmd.NewDefaultClientConfig(*credentials, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("could not load client configuration: %v", err)
	}
	return clusterConfig, nil
}

func (o *options) Run() error {
	start := time.Now()
	defer func() {
		log.Printf("Ran for %s", time.Now().Sub(start).Truncate(time.Second))
	}()
	var is *imageapi.ImageStream
	if !o.dry {
		projectGetter, err := versioned.NewForConfig(o.clusterConfig)
		if err != nil {
			return fmt.Errorf("could not get project client for cluster config: %v", err)
		}

		log.Printf("Creating namespace %s", o.namespace)
		for {
			project, err := projectGetter.ProjectV1().ProjectRequests().Create(&projectapi.ProjectRequest{
				ObjectMeta: meta.ObjectMeta{
					Name: o.namespace,
				},
				DisplayName: fmt.Sprintf("%s - %s", o.namespace, o.jobSpec.Job),
				Description: jobDescription(o.jobSpec, o.buildConfig),
			})
			if err != nil && !errors.IsAlreadyExists(err) {
				return fmt.Errorf("could not set up namespace for test: %v", err)
			}
			if err != nil {
				project, err = projectGetter.ProjectV1().Projects().Get(o.namespace, meta.GetOptions{})
				if err != nil {
					if errors.IsNotFound(err) {
						continue
					}
					return fmt.Errorf("cannot retrieve test namespace: %v", err)
				}
			}
			if project.Status.Phase == coreapi.NamespaceTerminating {
				log.Println("Waiting for namespace to finish terminating before creating another")
				time.Sleep(3 * time.Second)
				continue
			}
			break
		}

		if o.idleCleanupDuration > 0 {
			if err := o.createNamespaceCleanupPod(); err != nil {
				return err
			}
		}

		imageGetter, err := imageclientset.NewForConfig(o.clusterConfig)
		if err != nil {
			return fmt.Errorf("could not get image client for cluster config: %v", err)
		}

		// create the image stream or read it to get its uid
		is, err = imageGetter.ImageStreams(o.jobSpec.Namespace()).Create(&imageapi.ImageStream{
			ObjectMeta: meta.ObjectMeta{
				Namespace: o.jobSpec.Namespace(),
				Name:      steps.PipelineImageStream,
			},
			Spec: imageapi.ImageStreamSpec{
				// pipeline:* will now be directly referenceable
				LookupPolicy: imageapi.ImageLookupPolicy{Local: true},
			},
		})
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				return fmt.Errorf("could not set up pipeline imagestream for test: %v", err)
			}
			is, _ = imageGetter.ImageStreams(o.jobSpec.Namespace()).Get(steps.PipelineImageStream, meta.GetOptions{})
		}
		if is != nil {
			isTrue := true
			o.jobSpec.SetOwner(&meta.OwnerReference{
				APIVersion: "image.openshift.io/v1",
				Kind:       "ImageStream",
				Name:       steps.PipelineImageStream,
				UID:        is.UID,
				Controller: &isTrue,
			})
		}

		client, err := coreclientset.NewForConfig(o.clusterConfig)
		if err != nil {
			return fmt.Errorf("could not get core client for cluster config: %v", err)
		}
		for _, secret := range o.secrets {
			_, err := client.Secrets(o.namespace).Create(secret)
			if errors.IsAlreadyExists(err) {
				existing, err := client.Secrets(o.namespace).Get(secret.Name, meta.GetOptions{})
				if err != nil {
					return err
				}
				for k, v := range secret.Data {
					existing.Data[k] = v
				}
				if _, err := client.Secrets(o.namespace).Update(existing); err != nil {
					return err
				}
				log.Printf("Updated secret %s", secret.Name)
				continue
			}
			if err != nil {
				return err
			}
			log.Printf("Created secret %s", secret.Name)
		}
	}

	buildSteps, err := steps.FromConfig(o.buildConfig, o.jobSpec, o.templates, o.writeParams, o.clusterConfig)
	if err != nil {
		return fmt.Errorf("failed to generate steps from config: %v", err)
	}

	if err := steps.Run(api.BuildGraph(buildSteps), o.dry); err != nil {
		return err
	}

	return nil
}

// createNamespaceCleanupPod creates a pod that deletes the job namespace if no other run-once pods are running
// for more than idleCleanupDuration.
func (o *options) createNamespaceCleanupPod() error {
	log.Printf("Namespace will be deleted after %s of idle time", o.idleCleanupDuration)
	client, err := coreclientset.NewForConfig(o.clusterConfig)
	if err != nil {
		return fmt.Errorf("could not get image client for cluster config: %v", err)
	}
	rbacClient, err := rbacclientset.NewForConfig(o.clusterConfig)
	if err != nil {
		return fmt.Errorf("could not get image client for cluster config: %v", err)
	}

	if _, err := client.ServiceAccounts(o.namespace).Create(&coreapi.ServiceAccount{
		ObjectMeta: meta.ObjectMeta{
			Name: "cleanup",
		},
	}); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create service account for cleanup: %v", err)
	}
	if _, err := rbacClient.RoleBindings(o.namespace).Create(&rbacapi.RoleBinding{
		ObjectMeta: meta.ObjectMeta{
			Name: "cleanup",
		},
		Subjects: []rbacapi.Subject{{Kind: "ServiceAccount", Name: "cleanup"}},
		RoleRef: rbacapi.RoleRef{
			Kind: "ClusterRole",
			Name: "admin",
		},
	}); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create role binding for cleanup: %v", err)
	}

	grace := int64(30)
	deadline := int64(12 * time.Hour / time.Second)
	if _, err := client.Pods(o.namespace).Create(&coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name: "cleanup-when-idle",
		},
		Spec: coreapi.PodSpec{
			ActiveDeadlineSeconds:         &deadline,
			RestartPolicy:                 coreapi.RestartPolicyNever,
			TerminationGracePeriodSeconds: &grace,
			ServiceAccountName:            "cleanup",
			Containers: []coreapi.Container{
				{
					Name:  "cleanup",
					Image: "openshift/origin-cli:latest",
					Env: []coreapi.EnvVar{
						{
							Name:      "NAMESPACE",
							ValueFrom: &coreapi.EnvVarSource{FieldRef: &coreapi.ObjectFieldSelector{FieldPath: "metadata.namespace"}},
						},
						{
							Name:  "WAIT",
							Value: fmt.Sprintf("%d", int(o.idleCleanupDuration.Seconds())),
						},
					},
					Command: []string{"/bin/bash", "-c"},
					Args: []string{`
						#!/bin/bash
						set -euo pipefail

						function cleanup() {
							set +e
							oc delete project ${NAMESPACE}
						}

						trap 'kill $(jobs -p); echo "Pod deleted, deleting project ..."; exit 1' TERM
						trap cleanup EXIT

						echo "Waiting for all running pods to terminate (max idle ${WAIT}s) ..."
						count=0
						while true; do
							alive="$( oc get pods --template '{{ range .items }}{{ if and (not (eq .metadata.name "cleanup-when-idle")) (eq .spec.restartPolicy "Never") (or (eq .status.phase "Pending") (eq .status.phase "Running") (eq .status.phase "Unknown")) }} {{ .metadata.name }}{{ end }}{{ end }}' )"
							if [[ -n "${alive}" ]]; then
								count=0
								sleep ${WAIT} & wait
								continue
							fi
							if [[ "${count}" -lt 1 ]]; then
								count+=1
								sleep ${WAIT} & wait
								continue
							fi
							echo "No pods running for more than ${WAIT}s, deleting project ..."
							exit 0
						done
						`,
					},
				},
			},
		},
	}); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create pod for cleanup: %v", err)
	}
	return nil
}

// inputHash returns a string that hashes the unique parts of the input to avoid collisions.
func inputHash(job *steps.JobSpec, config *api.ReleaseBuildConfiguration) string {
	// only the ref spec off the job must be unique
	refSpec := job.RefSpec()
	// the entire config must be unique
	configSpec, err := json.Marshal(config)
	if err != nil {
		panic(err)
	}
	// Object names can't be too long so we truncate
	// the hash. This increases chances of collision
	// but we can tolerate it as our input space is
	// tiny.
	return fmt.Sprintf("%x", sha256.Sum256([]byte(refSpec+string(configSpec))))[54:]
}

// jobDescription returns a string representing the job's description.
func jobDescription(job *steps.JobSpec, config *api.ReleaseBuildConfiguration) string {
	var links []string
	for _, pull := range job.Refs.Pulls {
		links = append(links, fmt.Sprintf("https://github.com/%s/%s/pull/%d - %s", job.Refs.Org, job.Refs.Repo, pull.Number, pull.Author))
	}
	if len(links) > 0 {
		return fmt.Sprintf("%s on https://github.com/%s/%s\n\n%s", job.Job, job.Refs.Org, job.Refs.Repo, strings.Join(links, "\n"))
	}
	return fmt.Sprintf("%s on https://github.com/%s/%s ref=%s commit=%s", job.Job, job.Refs.Org, job.Refs.Repo, job.Refs.BaseRef, job.Refs.BaseSHA)
}
