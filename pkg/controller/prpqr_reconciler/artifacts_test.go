package prpqr_reconciler

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"k8s.io/utils/ptr"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
)

// artifactTestConfig provides distinct public and private decoration defaults.
func artifactTestConfig() *prowconfig.Config {
	return &prowconfig.Config{ProwConfig: prowconfig.ProwConfig{Plank: prowconfig.Plank{DefaultDecorationConfigs: []*prowconfig.DefaultDecorationConfigEntry{
		{Config: &prowv1.DecorationConfig{GCSConfiguration: &prowv1.GCSConfiguration{Bucket: "public-artifacts"}, GCSCredentialsSecret: ptr.To("public-writer")}},
		{OrgRepo: "openshift-priv", Config: &prowv1.DecorationConfig{GCSConfiguration: &prowv1.GCSConfiguration{Bucket: "private-artifacts"}, GCSCredentialsSecret: ptr.To("private-writer")}},
	}}}}
}

func TestSetPrivateArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		private, missingPrivate, missingCredentials bool
		refs                                        []prowv1.Refs
		bucket, secret                              string
		wantError                                   bool
	}{
		{name: "public unchanged", refs: []prowv1.Refs{{Org: "openshift", Repo: "component"}}, bucket: "public-artifacts", secret: "public-writer"},
		{name: "private only", private: true, refs: []prowv1.Refs{{Org: "openshift-priv", Repo: "component"}}, bucket: "private-artifacts", secret: "private-writer"},
		{name: "mixed public first", private: true, refs: []prowv1.Refs{{Org: "openshift", Repo: "first"}, {Org: "openshift-priv", Repo: "component"}}, bucket: "private-artifacts", secret: "private-writer"},
		{name: "reference free aggregator", private: true, bucket: "private-artifacts", secret: "private-writer"},
		{name: "missing private defaults fails closed", private: true, missingPrivate: true, wantError: true},
		{name: "missing private credentials fails closed", private: true, missingCredentials: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := artifactTestConfig()
			if tc.missingPrivate {
				config.Plank.DefaultDecorationConfigs = config.Plank.DefaultDecorationConfigs[:1]
			}
			if tc.missingCredentials {
				config.Plank.DefaultDecorationConfigs[1].Config.GCSCredentialsSecret = ptr.To("")
			}
			periodic := &prowconfig.Periodic{JobBase: prowconfig.JobBase{UtilityConfig: prowconfig.UtilityConfig{Decorate: ptr.To(true), ExtraRefs: tc.refs}}}
			err := setPrivateArtifacts(periodic, config, tc.private)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "private payload artifact storage") {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := config.DefaultPeriodic(periodic); err != nil {
				t.Fatal(err)
			}
			if periodic.DecorationConfig.GCSConfiguration.Bucket != tc.bucket || *periodic.DecorationConfig.GCSCredentialsSecret != tc.secret {
				t.Fatalf("unexpected artifact storage: %+v", periodic.DecorationConfig)
			}
			if periodic.Hidden != tc.private {
				t.Errorf("Hidden = %v, want %v", periodic.Hidden, tc.private)
			}
			if diff := cmp.Diff(tc.refs, periodic.ExtraRefs); diff != "" {
				t.Errorf("refs changed (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGenerateAggregatorJob(t *testing.T) {
	for _, tc := range []struct {
		name    string
		private bool
		bucket  string
	}{
		{name: "public"},
		{name: "private", private: true, bucket: "private-artifacts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getter := &fakeProwConfigGetter{cfg: artifactTestConfig()}
			job, err := generateAggregatorJob(&api.Metadata{Org: "openshift-priv", Repo: "component", Branch: "release-4.99"}, "id", "aggregator-test", "test", "run", "ci", getter, time.Unix(0, 0), "submitted", time.Hour, &fakeResolverClient{}, tc.private)
			if err != nil {
				t.Fatal(err)
			}
			if job.Spec.Hidden != tc.private {
				t.Fatalf("Hidden = %v", job.Spec.Hidden)
			}
			if len(job.Spec.ExtraRefs) != 0 {
				t.Fatal("aggregator unexpectedly clones refs")
			}
			found := false
			for _, env := range job.Spec.PodSpec.Containers[0].Env {
				if env.Name != "UNRESOLVED_CONFIG" {
					continue
				}
				found = true
				var config api.ReleaseBuildConfiguration
				if err := yaml.Unmarshal([]byte(env.Value), &config); err != nil {
					t.Fatal(err)
				}
				if got := config.Tests[0].MultiStageTestConfiguration.Environment["GOOGLE_STORAGE_BUCKET"]; got != tc.bucket {
					t.Errorf("reader bucket = %q, want %q", got, tc.bucket)
				}
				if config.Metadata.Org != "openshift-priv" || config.Metadata.Branch != "release-4.99" {
					t.Fatalf("runtime metadata changed: %+v", config.Metadata)
				}
			}
			if !found {
				t.Fatal("missing unresolved config")
			}
			if tc.private && job.Spec.DecorationConfig.GCSConfiguration.Bucket != tc.bucket {
				t.Fatal("aggregator artifacts are not private")
			}
		})
	}
}
