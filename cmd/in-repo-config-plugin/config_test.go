package main

import (
	"strings"
	"testing"
)

func TestGenerateBootstrapJobsDir(t *testing.T) {
	params := newBootstrapParams("openshift", "installer", "main", true, "quay.io/openshift/ci-operator-prowgen:latest", "quay.io/openshift/ci-operator-checkconfig:latest")
	jobConfig, err := generateBootstrapJobs(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	orgrepo := "openshift/installer"

	presubmits := jobConfig.PresubmitsStatic[orgrepo]
	if len(presubmits) != 1 {
		t.Fatalf("expected 1 presubmit, got %d", len(presubmits))
	}

	pre := presubmits[0]
	if pre.Name != "pull-ci-openshift-installer-main-ci-operator-config-check" {
		t.Errorf("expected presubmit name %q, got %q", "pull-ci-openshift-installer-main-ci-operator-config-check", pre.Name)
	}
	if len(pre.Branches) != 1 || pre.Branches[0] != "^main$" {
		t.Errorf("expected branch regex ^main$, got %v", pre.Branches)
	}
	if pre.RunIfChanged != `\.ci-operator(\.yaml|/)` {
		t.Errorf("expected run_if_changed %q, got %q", `\.ci-operator(\.yaml|/)`, pre.RunIfChanged)
	}
	if pre.AlwaysRun {
		t.Error("expected always_run to be false")
	}
	if pre.Context != "ci/prow/ci-operator-config-check" {
		t.Errorf("expected context %q, got %q", "ci/prow/ci-operator-config-check", pre.Context)
	}
	if pre.Spec == nil || len(pre.Spec.Containers) != 1 {
		t.Fatal("expected 1 container in presubmit spec")
	}
	if pre.Spec.Containers[0].Image != "quay.io/openshift/ci-operator-checkconfig:latest" {
		t.Errorf("unexpected image: %q", pre.Spec.Containers[0].Image)
	}
	if pre.Spec.Containers[0].Command[0] != "ci-operator-checkconfig" {
		t.Errorf("dir mode should use ci-operator-checkconfig directly, got %v", pre.Spec.Containers[0].Command)
	}
	repoPath := "/home/prow/go/src/github.com/openshift/installer"
	foundConfigDir := false
	for _, arg := range pre.Spec.Containers[0].Args {
		if arg == "--config-dir="+repoPath+"/.ci-operator" {
			foundConfigDir = true
		}
	}
	if !foundConfigDir {
		t.Errorf("expected --config-dir arg pointing to .ci-operator, got args %v", pre.Spec.Containers[0].Args)
	}
	if len(pre.ExtraRefs) != 1 || pre.ExtraRefs[0].Org != "openshift" || pre.ExtraRefs[0].Repo != "release" {
		t.Errorf("expected extra_refs for openshift/release, got %v", pre.ExtraRefs)
	}

	postsubmits := jobConfig.PostsubmitsStatic[orgrepo]
	if len(postsubmits) != 1 {
		t.Fatalf("expected 1 postsubmit, got %d", len(postsubmits))
	}

	post := postsubmits[0]
	if post.Name != "branch-ci-openshift-installer-main-prowgen" {
		t.Errorf("expected postsubmit name %q, got %q", "branch-ci-openshift-installer-main-prowgen", post.Name)
	}
	if post.MaxConcurrency != 1 {
		t.Errorf("expected max_concurrency 1, got %d", post.MaxConcurrency)
	}
	if post.Spec == nil || len(post.Spec.Containers) != 1 {
		t.Fatal("expected 1 container in postsubmit spec")
	}
	container := post.Spec.Containers[0]
	if container.Image != "quay.io/openshift/ci-operator-prowgen:latest" {
		t.Errorf("unexpected image: %q", container.Image)
	}
	if container.Command[0] != "ci-operator-prowgen" {
		t.Errorf("expected ci-operator-prowgen command, got %v", container.Command)
	}
	foundFromDir := false
	for _, arg := range container.Args {
		if arg == "--from-dir="+repoPath+"/.ci-operator" {
			foundFromDir = true
		}
	}
	if !foundFromDir {
		t.Errorf("expected --from-dir arg, got args %v", container.Args)
	}
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].MountPath != "/etc/jobs" {
		t.Error("expected volume mount at /etc/jobs")
	}
	if len(post.Spec.Volumes) != 1 || post.Spec.Volumes[0].PersistentVolumeClaim == nil {
		t.Error("expected PVC volume for job-configs")
	}
	if post.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "job-configs-nfs" {
		t.Errorf("expected PVC claim name job-configs-nfs, got %q", post.Spec.Volumes[0].PersistentVolumeClaim.ClaimName)
	}
}

func TestGenerateBootstrapJobsFile(t *testing.T) {
	params := newBootstrapParams("openshift", "installer", "main", false, "quay.io/openshift/ci-operator-prowgen:latest", "quay.io/openshift/ci-operator-checkconfig:latest")
	jobConfig, err := generateBootstrapJobs(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	orgrepo := "openshift/installer"
	pre := jobConfig.PresubmitsStatic[orgrepo][0]

	if pre.Spec.Containers[0].Command[0] != "/bin/sh" {
		t.Errorf("file mode should use /bin/sh, got %v", pre.Spec.Containers[0].Command)
	}
	shellCmd := pre.Spec.Containers[0].Args[0]
	if !strings.Contains(shellCmd, "mktemp -d") {
		t.Errorf("file mode should use mktemp, got %q", shellCmd)
	}
	if !strings.Contains(shellCmd, "cp /home/prow/go/src/github.com/openshift/installer/.ci-operator.yaml") {
		t.Errorf("file mode should copy .ci-operator.yaml, got %q", shellCmd)
	}
	if !strings.Contains(shellCmd, "ci-operator-checkconfig --config-dir=") {
		t.Errorf("file mode should run checkconfig, got %q", shellCmd)
	}

	post := jobConfig.PostsubmitsStatic[orgrepo][0]
	container := post.Spec.Containers[0]
	if container.Command[0] != "ci-operator-prowgen" {
		t.Errorf("expected ci-operator-prowgen command, got %v", container.Command)
	}
	foundFromFile := false
	for _, arg := range container.Args {
		if arg == "--from-file=/home/prow/go/src/github.com/openshift/installer/.ci-operator.yaml" {
			foundFromFile = true
		}
	}
	if !foundFromFile {
		t.Errorf("expected --from-file arg, got args %v", container.Args)
	}
}

func TestGenerateBootstrapJobsEscapesBranch(t *testing.T) {
	params := newBootstrapParams("org", "repo", "release-4.15", true, "img", "img")
	jobConfig, err := generateBootstrapJobs(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	orgrepo := "org/repo"

	pre := jobConfig.PresubmitsStatic[orgrepo][0]
	if pre.Branches[0] != `^release-4\.15$` {
		t.Errorf("expected escaped branch regex, got %q", pre.Branches[0])
	}

	post := jobConfig.PostsubmitsStatic[orgrepo][0]
	if post.Branches[0] != `^release-4\.15$` {
		t.Errorf("expected escaped branch regex, got %q", post.Branches[0])
	}

	if pre.Name != "pull-ci-org-repo-release-4.15-ci-operator-config-check" {
		t.Errorf("expected %q, got %q", "pull-ci-org-repo-release-4.15-ci-operator-config-check", pre.Name)
	}
}
