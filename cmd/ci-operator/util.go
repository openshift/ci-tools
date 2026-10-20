package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	authapi "k8s.io/api/authorization/v1"
	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	authclientset "k8s.io/client-go/kubernetes/typed/authorization/v1"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
)

// oneWayEncoding can be used to encode hex to a 62-character set (0 and 1 are duplicates) for use in
// short display names that are safe for use in kubernetes as resource names.
var oneWayNameEncoding = base32.NewEncoding("bcdfghijklmnpqrstvwxyz0123456789").WithPadding(base32.NoPadding)

// inputHash returns a string that hashes the unique parts of the input to avoid collisions.
func inputHash(inputs api.InputDefinition) string {
	hash := sha256.New()

	// the inputs form a part of the hash
	for _, s := range inputs {
		if _, err := hash.Write([]byte(s)); err != nil {
			logrus.WithError(err).Error("Failed to write hash.")
		}
	}

	// Object names can't be too long so we truncate
	// the hash. This increases chances of collision
	// but we can tolerate it as our input space is
	// tiny.
	return oneWayNameEncoding.EncodeToString(hash.Sum(nil)[:5])
}

var shaRegex = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// shorten takes a string, and if it looks like a hexadecimal Git SHA it truncates it to
// l characters. The values provided to job spec are not required to be SHAs but could also be
// tags or other git refs.
func shorten(value string, l int) string {
	if len(value) > l && shaRegex.MatchString(value) {
		return value[:l]
	}
	return value
}

func summarizeRef(refs prowapi.Refs) string {
	if len(refs.Pulls) > 0 {
		var pulls []string
		for _, pull := range refs.Pulls {
			pulls = append(pulls, fmt.Sprintf("#%d %s @%s", pull.Number, shorten(pull.SHA, 8), pull.Author))
		}
		return fmt.Sprintf("Resolved source https://github.com/%s/%s to %s@%s, merging: %s", refs.Org, refs.Repo, refs.BaseRef, shorten(refs.BaseSHA, 8), strings.Join(pulls, ", "))
	}
	if refs.BaseSHA == "" {
		return fmt.Sprintf("Resolved SHA missing for %s in https://github.com/%s/%s (will prevent caching)", refs.BaseRef, refs.Org, refs.Repo)
	}
	return fmt.Sprintf("Resolved source https://github.com/%s/%s to %s@%s", refs.Org, refs.Repo, refs.BaseRef, shorten(refs.BaseSHA, 8))
}

// eventJobDescription returns a string representing the pull requests and authors description, to be used in events.
func eventJobDescription(jobSpec *api.JobSpec, namespace string) string {
	var pulls []string
	var authors []string

	if jobSpec.Refs == nil {
		return fmt.Sprintf("Running job %s in namespace %s", jobSpec.Job, namespace)
	}
	if len(jobSpec.Refs.Pulls) == 1 {
		pull := jobSpec.Refs.Pulls[0]
		return fmt.Sprintf("Running job %s for PR https://github.com/%s/%s/pull/%d in namespace %s from author %s",
			jobSpec.Job, jobSpec.Refs.Org, jobSpec.Refs.Repo, pull.Number, namespace, pull.Author)
	}
	for _, pull := range jobSpec.Refs.Pulls {
		pulls = append(pulls, fmt.Sprintf("https://github.com/%s/%s/pull/%d", jobSpec.Refs.Org, jobSpec.Refs.Repo, pull.Number))
		authors = append(authors, pull.Author)
	}
	return fmt.Sprintf("Running job %s for PRs (%s) in namespace %s from authors (%s)",
		jobSpec.Job, strings.Join(pulls, ", "), namespace, strings.Join(authors, ", "))
}

// jobDescription returns a string representing the job's description.
func jobDescription(job *api.JobSpec) string {
	if job.Refs == nil {
		return job.Job
	}
	var links []string
	for _, pull := range job.Refs.Pulls {
		links = append(links, fmt.Sprintf("https://github.com/%s/%s/pull/%d - %s", job.Refs.Org, job.Refs.Repo, pull.Number, pull.Author))
	}
	if len(links) > 0 {
		return fmt.Sprintf("%s\n\n%s on https://github.com/%s/%s", strings.Join(links, "\n"), job.Job, job.Refs.Org, job.Refs.Repo)
	}
	return fmt.Sprintf("%s on https://github.com/%s/%s ref=%s commit=%s", job.Job, job.Refs.Org, job.Refs.Repo, job.Refs.BaseRef, job.Refs.BaseSHA)
}

func jobSpecFromGitRef(ref string) (*api.JobSpec, error) {
	parts := strings.Split(ref, "@")
	if len(parts) != 2 {
		return nil, fmt.Errorf("must be ORG/NAME@REF")
	}
	prefix := strings.Split(parts[0], "/")
	if len(prefix) != 2 {
		return nil, fmt.Errorf("must be ORG/NAME@REF")
	}
	repo := fmt.Sprintf("https://github.com/%s/%s.git", prefix[0], prefix[1])
	out, err := exec.Command("git", "ls-remote", repo, parts[1]).Output()
	if err != nil {
		return nil, fmt.Errorf("'git ls-remote %s %s' failed with '%w'", repo, parts[1], err)
	}
	resolved := strings.Split(strings.Split(string(out), "\n")[0], "\t")
	sha := resolved[0]
	if len(sha) == 0 {
		return nil, fmt.Errorf("ref '%s' does not point to any commit in '%s'", parts[1], parts[0])
	}
	// sanity check that regular refs are fully determined
	if strings.HasPrefix(resolved[1], "refs/heads/") && !strings.HasPrefix(parts[1], "refs/heads/") {
		if resolved[1] != ("refs/heads/" + parts[1]) {
			trimmed := resolved[1][len("refs/heads/"):]
			// we could fix this for the user, but better to require them to be explicit
			return nil, fmt.Errorf("ref '%s' does not point to any commit in '%s' (did you mean '%s'?)", parts[1], parts[0], trimmed)
		}
	}
	logrus.Infof("Resolved %s to commit %s", ref, sha)
	spec := &api.JobSpec{
		JobSpec: downwardapi.JobSpec{
			Type: prowapi.PeriodicJob,
			Job:  "dev",
			Refs: &prowapi.Refs{
				Org:     prefix[0],
				Repo:    prefix[1],
				BaseRef: parts[1],
				BaseSHA: sha,
			},
		}}
	return spec, nil
}

func eventRecorder(kubeClient *coreclientset.CoreV1Client, authClient *authclientset.AuthorizationV1Client, namespace string) (record.EventRecorder, error) {
	res, err := authClient.SelfSubjectAccessReviews().Create(context.TODO(), &authapi.SelfSubjectAccessReview{
		Spec: authapi.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authapi.ResourceAttributes{
				Namespace: namespace,
				Verb:      "create",
				Resource:  "events",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not check permission to create events: %w", err)
	}
	if !res.Status.Allowed {
		logrus.Warn("Events will not be created because of lack of permission.")
		return &record.FakeRecorder{}, nil
	}
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&coreclientset.EventSinkImpl{
		Interface: coreclientset.New(kubeClient.RESTClient()).Events("")})
	return eventBroadcaster.NewRecorder(
		scheme.Scheme, coreapi.EventSource{Component: namespace}), nil
}

func getCloneSecretFromPath(cloneAuthType steps.CloneAuthType, secretPath string) (*coreapi.Secret, error) {
	secret := &coreapi.Secret{Data: make(map[string][]byte)}
	data, err := os.ReadFile(secretPath)
	if err != nil {
		return nil, fmt.Errorf("could not read file %s for secret: %w", secretPath, err)
	}
	hash := getHashFromBytes(data)
	data = bytes.TrimSpace(data)

	if cloneAuthType == steps.CloneAuthTypeSSH {
		secret.Name = fmt.Sprintf("ssh-%s", hash)
		secret.Type = coreapi.SecretTypeSSHAuth
		// Secret.Data["ssh-privatekey"] is required on SecretTypeSSHAuth type.
		// https://github.com/kubernetes/api/blob/master/core/v1/types.go#L5466-L5470
		secret.Data[coreapi.SSHAuthPrivateKey] = data
	} else if cloneAuthType == steps.CloneAuthTypeOAuth {
		secret.Type = coreapi.SecretTypeBasicAuth
		secret.Name = fmt.Sprintf("oauth-%s", hash)
		secret.Data[steps.OauthSecretKey] = data

		// Those keys will be used in a git source strategy build
		secret.Data["username"] = data
		secret.Data["password"] = data
	}

	return secret, nil
}

func getHashFromBytes(b []byte) string {
	hash := sha256.New()
	if _, err := hash.Write(b); err != nil {
		logrus.WithError(err).Error("Failed to write hash.")
	}
	return oneWayNameEncoding.EncodeToString(hash.Sum(nil)[:5])
}

func getDockerConfigSecret(name, filename string) (*coreapi.Secret, error) {
	src, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("could not read file %s for secret %s: %w", filename, name, err)
	}
	return &coreapi.Secret{
		Data: map[string][]byte{
			coreapi.DockerConfigJsonKey: src,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Type: coreapi.SecretTypeDockerConfigJson,
	}, nil
}

func getSecret(name, filename string) (*coreapi.Secret, error) {
	src, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("could not read file %s for secret %s: %w", filename, name, err)
	}
	return &coreapi.Secret{
		Data: map[string][]byte{
			path.Base(filename): src,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Type: coreapi.SecretTypeOpaque,
	}, nil
}

func resolveGCSCredentialsSecret(jobSpec *api.JobSpec) string {
	if jobSpec.DecorationConfig != nil && jobSpec.DecorationConfig.GCSCredentialsSecret != nil {
		return *jobSpec.DecorationConfig.GCSCredentialsSecret
	}

	return api.GCSUploadCredentialsSecret
}

func resolveNodeArchitectures(ctx context.Context, client coreclientset.NodeInterface) ([]string, error) {
	ret := sets.New[string]()
	nodeList, err := client.List(ctx, metav1.ListOptions{})

	if err != nil {
		return nil, fmt.Errorf("failed to determine the node architectures: %w", err)
	}

	for _, node := range nodeList.Items {
		ret.Insert(node.Status.NodeInfo.Architecture)
	}
	return sets.List(ret), nil
}
