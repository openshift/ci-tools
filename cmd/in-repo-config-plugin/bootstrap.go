package main

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

func generateBootstrapJobs(org, repo, branch, prowgenImage, checkconfigImage string) *prowconfig.JobConfig {
	orgrepo := fmt.Sprintf("%s/%s", org, repo)
	branchRegex := jc.ExactlyBranch(branch)

	return &prowconfig.JobConfig{
		PresubmitsStatic: map[string][]prowconfig.Presubmit{
			orgrepo: {generateConfigCheckerPresubmit(org, repo, branch, branchRegex, checkconfigImage)},
		},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
			orgrepo: {generateProwgenPostsubmit(org, repo, branch, branchRegex, prowgenImage)},
		},
	}
}

func generateConfigCheckerPresubmit(org, repo, branch, branchRegex, image string) prowconfig.Presubmit {
	name := fmt.Sprintf("pull-ci-%s-%s-%s-ci-operator-config-check", org, repo, branch)
	trueBool := true
	repoPath := fmt.Sprintf("/home/prow/go/src/github.com/%s/%s", org, repo)

	return prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Name:  name,
			Agent: string(prowv1.KubernetesAgent),
			Labels: map[string]string{
				jc.LabelGenerator: string(pluginGenerator),
			},
			Spec: &corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:    "checkconfig",
					Image:   image,
					Command: []string{"ci-operator-checkconfig"},
					Args: []string{
						fmt.Sprintf("--config-dir=%s/%s", repoPath, ciOperatorDir),
					},
				}},
			},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate: &trueBool,
			},
		},
		Brancher: prowconfig.Brancher{
			Branches: []string{branchRegex},
		},
		RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{
			RunIfChanged: `\.ci-operator/`,
		},
		AlwaysRun: false,
	}
}

func generateProwgenPostsubmit(org, repo, branch, branchRegex, image string) prowconfig.Postsubmit {
	name := fmt.Sprintf("branch-ci-%s-%s-%s-prowgen", org, repo, branch)
	trueBool := true
	repoPath := fmt.Sprintf("/home/prow/go/src/github.com/%s/%s", org, repo)

	return prowconfig.Postsubmit{
		JobBase: prowconfig.JobBase{
			Name:  name,
			Agent: string(prowv1.KubernetesAgent),
			Labels: map[string]string{
				jc.LabelGenerator: string(pluginGenerator),
			},
			Spec: &corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:    "prowgen",
					Image:   image,
					Command: []string{"ci-operator-prowgen"},
					Args: []string{
						fmt.Sprintf("--from-file=%s/.ci-operator.yaml", repoPath),
						"--to-dir=/etc/jobs",
					},
					VolumeMounts: []corev1.VolumeMount{{
						Name:      "job-configs",
						MountPath: "/etc/jobs",
					}},
				}},
				Volumes: []corev1.Volume{{
					Name: "job-configs",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: "job-configs-nfs",
						},
					},
				}},
			},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate: &trueBool,
			},
			MaxConcurrency: 1,
		},
		Brancher: prowconfig.Brancher{
			Branches: []string{branchRegex},
		},
	}
}
