package deprecatetemplates

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/plugins"
)

func TestLoadTemplates(t *testing.T) {
	testcases := []struct {
		description string
		updaterCfg  plugins.ConfigUpdater
		expected    sets.String
	}{
		{
			description: "template is detected",
			updaterCfg: plugins.ConfigUpdater{
				Maps: map[string]plugins.ConfigMapSpec{
					"ci-operator/templates/this-is-a-template.yaml": {Name: "template"},
				},
			},
			expected: sets.NewString("template"),
		},
		{
			description: "not a template is ignored",
			updaterCfg: plugins.ConfigUpdater{
				Maps: map[string]plugins.ConfigMapSpec{
					"ci-operator/config/this/is-not/a-template.yaml": {Name: "not-a-template"},
				},
			},
			expected: sets.NewString(),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.description, func(t *testing.T) {
			enforcer := Enforcer{}
			enforcer.LoadTemplates(tc.updaterCfg)
			if diff := cmp.Diff(tc.expected, enforcer.existingTemplates); diff != "" {
				t.Errorf("%s: templates differ from expected:\n%s", tc.description, diff)
			}
		})
	}
}

type mockAllowlist struct {
	jobs map[string]sets.String
}

func (m *mockAllowlist) Insert(job config.JobBase, template string) {
	if _, ok := m.jobs[template]; !ok {
		m.jobs[template] = sets.NewString()
	}
	m.jobs[template].Insert(job.Name)
}

func (m *mockAllowlist) Save(_ string) error {
	panic("this should never be called")
}

type mockJobConfig struct {
	presubmits  []config.Presubmit
	postsubmits []config.Postsubmit
	periodics   []config.Periodic
}

func (m *mockJobConfig) AllStaticPostsubmits(_ []string) []config.Postsubmit {
	return append([]config.Postsubmit{}, m.postsubmits...)
}
func (m *mockJobConfig) AllStaticPresubmits(_ []string) []config.Presubmit {
	return append([]config.Presubmit{}, m.presubmits...)
}
func (m *mockJobConfig) AllPeriodics() []config.Periodic {
	return append([]config.Periodic{}, m.periodics...)
}

func cmVolume(name, cmName string) corev1.Volume {
	return corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
			},
		},
	}
}

func TestProcessJobs(t *testing.T) {
	template := "template"
	jobWithTemplate := config.JobBase{
		Name: "job-with-template",
		Spec: &corev1.PodSpec{
			Volumes: []corev1.Volume{cmVolume("volume", template)},
		},
	}
	jobWithoutTemplate := config.JobBase{
		Name: "job-without-template",
		Spec: &corev1.PodSpec{},
	}

	testcases := []struct {
		description string
		presubmits  []config.Presubmit
		postsubmits []config.Postsubmit
		periodics   []config.Periodic

		inserted sets.String
	}{
		{
			description: "presubmit using template is added",
			presubmits:  []config.Presubmit{{JobBase: jobWithTemplate}},
			inserted:    sets.NewString("job-with-template"),
		},
		{
			description: "postsubmit using template is added",
			postsubmits: []config.Postsubmit{{JobBase: jobWithTemplate}},
			inserted:    sets.NewString("job-with-template"),
		},
		{
			description: "periodics using template is added",
			periodics:   []config.Periodic{{JobBase: jobWithTemplate}},
			inserted:    sets.NewString("job-with-template"),
		},
		{
			description: "jobs not using template are ignored",
			presubmits:  []config.Presubmit{{JobBase: jobWithTemplate}, {JobBase: jobWithoutTemplate}},
			postsubmits: []config.Postsubmit{{JobBase: jobWithoutTemplate}},
			periodics:   []config.Periodic{{JobBase: jobWithoutTemplate}},
			inserted:    sets.NewString("job-with-template"),
		},
	}

	for _, tc := range testcases {
		mock := mockAllowlist{jobs: map[string]sets.String{}}
		mockJobs := &mockJobConfig{
			presubmits:  tc.presubmits,
			postsubmits: tc.postsubmits,
			periodics:   tc.periodics,
		}
		t.Run(tc.description, func(t *testing.T) {
			enforcer := Enforcer{
				existingTemplates: sets.NewString(template),
				allowlist:         &mock,
			}
			enforcer.ProcessJobs(mockJobs)

			if jobs, ok := mock.jobs[template]; !ok {
				t.Errorf("%s: no record added for template '%s'", tc.description, template)
			} else if diff := cmp.Diff(jobs, tc.inserted); diff != "" {
				t.Errorf("%s: inserted jobs differ:\n%s", tc.description, diff)
			}
		})
	}
}
