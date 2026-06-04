package main

import (
	"testing"
)

func TestGenerateBootstrapJobs(t *testing.T) {
	org := "openshift"
	repo := "installer"
	branch := "main"
	prowgenImage := "quay.io/openshift/ci-operator-prowgen:latest"
	checkconfigImage := "quay.io/openshift/ci-operator-checkconfig:latest"

	jobConfig := generateBootstrapJobs(org, repo, branch, prowgenImage, checkconfigImage)

	orgrepo := "openshift/installer"

	presubmits := jobConfig.PresubmitsStatic[orgrepo]
	if len(presubmits) != 1 {
		t.Fatalf("expected 1 presubmit, got %d", len(presubmits))
	}

	pre := presubmits[0]
	expectedPreName := "pull-ci-openshift-installer-main-ci-operator-config-check"
	if pre.Name != expectedPreName {
		t.Errorf("expected presubmit name %q, got %q", expectedPreName, pre.Name)
	}
	if len(pre.Branches) != 1 || pre.Branches[0] != "^main$" {
		t.Errorf("expected branch regex ^main$, got %v", pre.Branches)
	}
	if pre.RunIfChanged != `\.ci-operator/` {
		t.Errorf("expected run_if_changed %q, got %q", `\.ci-operator/`, pre.RunIfChanged)
	}
	if pre.AlwaysRun {
		t.Error("expected always_run to be false")
	}
	if pre.Spec == nil || len(pre.Spec.Containers) != 1 {
		t.Fatal("expected 1 container in presubmit spec")
	}
	if pre.Spec.Containers[0].Image != checkconfigImage {
		t.Errorf("expected image %q, got %q", checkconfigImage, pre.Spec.Containers[0].Image)
	}

	postsubmits := jobConfig.PostsubmitsStatic[orgrepo]
	if len(postsubmits) != 1 {
		t.Fatalf("expected 1 postsubmit, got %d", len(postsubmits))
	}

	post := postsubmits[0]
	expectedPostName := "branch-ci-openshift-installer-main-prowgen"
	if post.Name != expectedPostName {
		t.Errorf("expected postsubmit name %q, got %q", expectedPostName, post.Name)
	}
	if len(post.Branches) != 1 || post.Branches[0] != "^main$" {
		t.Errorf("expected branch regex ^main$, got %v", post.Branches)
	}
	if post.MaxConcurrency != 1 {
		t.Errorf("expected max_concurrency 1, got %d", post.MaxConcurrency)
	}
	if post.Spec == nil || len(post.Spec.Containers) != 1 {
		t.Fatal("expected 1 container in postsubmit spec")
	}
	container := post.Spec.Containers[0]
	if container.Image != prowgenImage {
		t.Errorf("expected image %q, got %q", prowgenImage, container.Image)
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

func TestGenerateBootstrapJobsEscapesBranch(t *testing.T) {
	jobConfig := generateBootstrapJobs("org", "repo", "release-4.15", "img", "img")
	orgrepo := "org/repo"

	pre := jobConfig.PresubmitsStatic[orgrepo][0]
	if pre.Branches[0] != `^release-4\.15$` {
		t.Errorf("expected escaped branch regex, got %q", pre.Branches[0])
	}

	post := jobConfig.PostsubmitsStatic[orgrepo][0]
	if post.Branches[0] != `^release-4\.15$` {
		t.Errorf("expected escaped branch regex, got %q", post.Branches[0])
	}

	expectedPreName := "pull-ci-org-repo-release-4.15-ci-operator-config-check"
	if pre.Name != expectedPreName {
		t.Errorf("expected %q, got %q", expectedPreName, pre.Name)
	}
}
