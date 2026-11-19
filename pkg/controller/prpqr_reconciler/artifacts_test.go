package prpqr_reconciler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	v1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
)

// artifactTestConfig provides distinct public and private decoration defaults.
func artifactTestConfig() *prowconfig.Config {
	return &prowconfig.Config{ProwConfig: prowconfig.ProwConfig{Plank: prowconfig.Plank{DefaultDecorationConfigs: []*prowconfig.DefaultDecorationConfigEntry{
		{Config: &prowv1.DecorationConfig{GCSConfiguration: &prowv1.GCSConfiguration{Bucket: "public-artifacts"}, GCSCredentialsSecret: ptr.To("public-writer")}},
		{OrgRepo: "openshift-priv", Config: &prowv1.DecorationConfig{GCSConfiguration: &prowv1.GCSConfiguration{Bucket: "private-artifacts"}, GCSCredentialsSecret: ptr.To("private-writer")}},
	}}}}
}

func TestParseArtifactBucket(t *testing.T) {
	for _, tc := range []struct {
		name, input, identity string
		wantError             bool
	}{
		{name: "bare GCS", input: "artifacts", identity: "gs://artifacts"},
		{name: "explicit GCS", input: "gs://artifacts", identity: "gs://artifacts"},
		{name: "uppercase scheme", input: "GS://artifacts", identity: "gs://artifacts"},
		{name: "GCS prefix", input: "artifacts/prefix", identity: "gs://artifacts"},
		{name: "S3 prefix", input: "s3://artifacts/prefix", identity: "s3://artifacts"},
		{name: "empty", wantError: true},
		{name: "missing host", input: "gs:///prefix", wantError: true},
		{name: "invalid escape", input: "gs://%zz", wantError: true},
		{name: "unsupported scheme", input: "https://artifacts", wantError: true},
		{name: "userinfo", input: "gs://user@artifacts", wantError: true},
		{name: "query", input: "gs://artifacts?query", wantError: true},
		{name: "empty query", input: "gs://artifacts?", wantError: true},
		{name: "fragment", input: "gs://artifacts#fragment", wantError: true},
		{name: "empty fragment", input: "gs://artifacts#", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := parseArtifactBucket(tc.input)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "artifact bucket") {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := parsed.BucketWithScheme(); got != tc.identity {
				t.Errorf("identity = %q, want %q", got, tc.identity)
			}
		})
	}
}

func TestPrivateAggregationBucket(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		wantError   bool
	}{
		{name: "bare", input: "artifacts"},
		{name: "GCS URI", input: "gs://artifacts"},
		{name: "different provider", input: "s3://artifacts", wantError: true},
		{name: "prefix", input: "gs://artifacts/prefix", wantError: true},
		{name: "trailing slash", input: "gs://artifacts/", wantError: true},
		{name: "malformed", input: "gs://", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bucket, err := privateAggregationBucket(tc.input)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "private aggregation") {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if bucket != "artifacts" {
				t.Errorf("bucket = %q, want artifacts", bucket)
			}
		})
	}
}

func TestSetPrivateArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		private, missingPrivate, missingCredentials bool
		refs                                        []prowv1.Refs
		bucket, secret                              string
		wantError                                   bool
		publicScope, publicBucket, publicSecret     string
		cluster, privateCluster                     string
		globalBucket, privateBucket                 string
	}{
		{name: "public unchanged", refs: []prowv1.Refs{{Org: "openshift", Repo: "component"}}, bucket: "public-artifacts", secret: "public-writer"},
		{name: "private only", private: true, refs: []prowv1.Refs{{Org: "openshift-priv", Repo: "component"}}, bucket: "private-artifacts", secret: "private-writer"},
		{name: "mixed public first", private: true, refs: []prowv1.Refs{{Org: "openshift", Repo: "first"}, {Org: "openshift-priv", Repo: "component"}}, bucket: "private-artifacts", secret: "private-writer"},
		{name: "reference free aggregator", private: true, bucket: "private-artifacts", secret: "private-writer"},
		{name: "missing private defaults fails closed", private: true, missingPrivate: true, wantError: true},
		{name: "missing private credentials fails closed", private: true, missingCredentials: true, wantError: true},
		{name: "public org shares private bucket", private: true, publicScope: "openshift", publicBucket: "private-artifacts", wantError: true},
		{name: "public org shares private writer", private: true, publicScope: "openshift", publicSecret: "private-writer", wantError: true},
		{name: "flattened public org shares bucket", private: true, publicScope: "operator-framework", publicBucket: "private-artifacts", wantError: true},
		{name: "public repo shares bucket", private: true, publicScope: "example/component", publicBucket: "private-artifacts", wantError: true},
		{name: "distinct public org storage", private: true, publicScope: "openshift", publicBucket: "other-public", publicSecret: "other-writer", bucket: "private-artifacts", secret: "private-writer"},
		{name: "public org collision does not change public jobs", publicScope: "openshift", publicBucket: "private-artifacts", refs: []prowv1.Refs{{Org: "openshift", Repo: "component"}}, bucket: "private-artifacts", secret: "public-writer"},
		{name: "default cluster private storage", private: true, privateCluster: "default", bucket: "private-artifacts", secret: "private-writer"},
		{name: "public URI private bare collision", private: true, globalBucket: "gs://shared-artifacts", privateBucket: "shared-artifacts", wantError: true},
		{name: "public bare private URI collision", private: true, globalBucket: "shared-artifacts", privateBucket: "gs://shared-artifacts", wantError: true},
		{name: "scheme case does not isolate buckets", private: true, globalBucket: "GS://shared-artifacts", privateBucket: "shared-artifacts", wantError: true},
		{name: "public org URI collision", private: true, publicScope: "openshift", publicBucket: "gs://private-artifacts", wantError: true},
		{name: "public repository bare collision", private: true, publicScope: "example/component", publicBucket: "private-artifacts", privateBucket: "gs://private-artifacts", wantError: true},
		{name: "different prefixes do not isolate a bucket", private: true, globalBucket: "gs://shared-artifacts/public", privateBucket: "shared-artifacts/private", wantError: true},
		{name: "distinct URI buckets accepted", private: true, globalBucket: "gs://public-artifacts", privateBucket: "gs://private-artifacts", bucket: "gs://private-artifacts", secret: "private-writer"},
		{name: "distinct providers accepted", private: true, globalBucket: "s3://private-artifacts", bucket: "private-artifacts", secret: "private-writer"},
		{name: "private prefix with distinct bucket accepted", private: true, privateBucket: "gs://private-artifacts/prefix", bucket: "gs://private-artifacts/prefix", secret: "private-writer"},
		{name: "private missing host rejected", private: true, privateBucket: "gs://", wantError: true},
		{name: "private malformed URL rejected", private: true, privateBucket: "gs://%zz", wantError: true},
		{name: "private query rejected", private: true, privateBucket: "gs://private-artifacts?query", wantError: true},
		{name: "private fragment rejected", private: true, privateBucket: "gs://private-artifacts#fragment", wantError: true},
		{name: "private userinfo rejected", private: true, privateBucket: "gs://user@private-artifacts", wantError: true},
		{name: "unsupported scheme rejected", private: true, privateBucket: "gcs://private-artifacts", wantError: true},
		{name: "malformed public destination fails closed", private: true, publicScope: "openshift", publicBucket: "gs://", wantError: true},
		{name: "public malformed destination unchanged", globalBucket: "gs://", bucket: "gs://", secret: "public-writer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := artifactTestConfig()
			if tc.globalBucket != "" {
				config.Plank.DefaultDecorationConfigs[0].Config.GCSConfiguration.Bucket = tc.globalBucket
			}
			if tc.privateBucket != "" {
				config.Plank.DefaultDecorationConfigs[1].Config.GCSConfiguration.Bucket = tc.privateBucket
			}
			config.Plank.DefaultDecorationConfigs[1].Cluster = tc.privateCluster
			if tc.publicScope != "" {
				public := &prowv1.DecorationConfig{}
				if tc.publicBucket != "" {
					public.GCSConfiguration = &prowv1.GCSConfiguration{Bucket: tc.publicBucket}
				}
				if tc.publicSecret != "" {
					public.GCSCredentialsSecret = ptr.To(tc.publicSecret)
				}
				config.Plank.DefaultDecorationConfigs = append(config.Plank.DefaultDecorationConfigs, &prowconfig.DefaultDecorationConfigEntry{OrgRepo: tc.publicScope, Config: public})
			}
			if tc.missingPrivate {
				config.Plank.DefaultDecorationConfigs = config.Plank.DefaultDecorationConfigs[:1]
			}
			if tc.missingCredentials {
				config.Plank.DefaultDecorationConfigs[1].Config.GCSCredentialsSecret = ptr.To("")
			}
			periodic := &prowconfig.Periodic{JobBase: prowconfig.JobBase{Cluster: tc.cluster, UtilityConfig: prowconfig.UtilityConfig{Decorate: ptr.To(true), ExtraRefs: tc.refs}}}
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
		name             string
		private          bool
		bucket           string
		configuredBucket string
	}{
		{name: "public"},
		{name: "private", private: true, bucket: "private-artifacts"},
		{name: "private URI reader gets bare bucket", private: true, configuredBucket: "gs://private-artifacts", bucket: "private-artifacts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getter := &fakeProwConfigGetter{cfg: artifactTestConfig()}
			if tc.configuredBucket != "" {
				getter.cfg.Plank.DefaultDecorationConfigs[1].Config.GCSConfiguration.Bucket = tc.configuredBucket
			}
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
			writerBucket := tc.bucket
			if tc.configuredBucket != "" {
				writerBucket = tc.configuredBucket
			}
			if tc.private && job.Spec.DecorationConfig.GCSConfiguration.Bucket != writerBucket {
				t.Fatal("aggregator artifacts are not private")
			}
		})
	}
}

// artifactProwConfigGetter uses Prow's actual periodic defaulting consumer.
type artifactProwConfigGetter struct {
	*fakeProwConfigGetter
}

func (getter artifactProwConfigGetter) Defaulter() periodicDefaulter {
	return getter.cfg
}

func TestTriggerJobs(t *testing.T) {
	for _, tc := range []struct {
		name, workerBucket, aggregatorBucket string
		private                              bool
		wantJobs                             int
		errorText                            string
	}{
		{name: "private matching buckets across clusters", private: true, workerBucket: "private-artifacts", aggregatorBucket: "private-artifacts", wantJobs: 3},
		{name: "private mismatched buckets create no jobs", private: true, workerBucket: "private-worker-artifacts", aggregatorBucket: "private-aggregator-artifacts", errorText: "private aggregation artifact buckets differ"},
		{name: "public differing private defaults unchanged", workerBucket: "private-worker-artifacts", aggregatorBucket: "private-aggregator-artifacts", wantJobs: 3},
		{name: "private worker URI matches bare aggregator", private: true, workerBucket: "gs://private-artifacts", aggregatorBucket: "private-artifacts", wantJobs: 3},
		{name: "private bare worker matches URI aggregator", private: true, workerBucket: "private-artifacts", aggregatorBucket: "gs://private-artifacts", wantJobs: 3},
		{name: "private uppercase worker scheme matches aggregator", private: true, workerBucket: "GS://private-artifacts", aggregatorBucket: "private-artifacts", wantJobs: 3},
		{name: "private equal prefixes are not reader compatible", private: true, workerBucket: "gs://private-artifacts/prefix", aggregatorBucket: "gs://private-artifacts/prefix", errorText: "private aggregation"},
		{name: "private worker prefix rejected", private: true, workerBucket: "private-artifacts/prefix", aggregatorBucket: "private-artifacts", errorText: "private aggregation"},
		{name: "private s3 aggregation unsupported by reader", private: true, workerBucket: "s3://private-artifacts", aggregatorBucket: "s3://private-artifacts", errorText: "private aggregation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := artifactTestConfig()
			config.Plank.DefaultDecorationConfigs[1].Config.GCSConfiguration.Bucket = tc.aggregatorBucket
			config.Plank.DefaultDecorationConfigs = append(config.Plank.DefaultDecorationConfigs, &prowconfig.DefaultDecorationConfigEntry{OrgRepo: "openshift-priv", Cluster: "build02", Config: &prowv1.DecorationConfig{GCSConfiguration: &prowv1.GCSConfiguration{Bucket: tc.workerBucket}}})
			scheme := runtime.NewScheme()
			if err := prowv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			client := fake.NewClientBuilder().WithScheme(scheme).Build()
			r := &reconciler{client: client, configResolverClient: &fakeResolverClient{}, prowConfigGetter: artifactProwConfigGetter{&fakeProwConfigGetter{cfg: config}}, dispatcherClient: &fakeDispatcherClient{}, jobTriggerWaitDuration: time.Second, defaultAggregatorJobTimeout: time.Hour}
			run := &v1.PullRequestPayloadQualificationRun{ObjectMeta: metav1.ObjectMeta{Name: "run", Namespace: "ci"}, Spec: v1.PullRequestPayloadTestSpec{Private: tc.private, PullRequests: []v1.PullRequestUnderTest{{Org: "openshift-priv", Repo: "component", BaseRef: "release-4.99", BaseSHA: "base-sha", PullRequest: &v1.PullRequest{Number: 1, SHA: "head-sha"}}}, Jobs: v1.PullRequestPayloadJobSpec{Jobs: []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "main"}, Test: "test", AggregatedCount: 2}}}}}
			statuses := map[string]*v1.PullRequestPayloadJobStatus{}
			r.triggerJobs(context.Background(), logrus.NewEntry(logrus.New()), reconcile.Request{NamespacedName: types.NamespacedName{Name: run.Name, Namespace: run.Namespace}}, run, &prowv1.ProwJobList{}, statuses, time.Millisecond)
			var jobs prowv1.ProwJobList
			if err := client.List(context.Background(), &jobs); err != nil {
				t.Fatal(err)
			}
			if len(jobs.Items) != tc.wantJobs {
				t.Fatalf("created %d jobs, want %d", len(jobs.Items), tc.wantJobs)
			}
			if len(statuses) != 1 {
				t.Fatalf("got %d statuses, want 1", len(statuses))
			}
			for _, status := range statuses {
				if tc.errorText != "" {
					if status.Status.State != prowv1.ErrorState || !strings.Contains(status.Status.Description, tc.errorText) {
						t.Fatalf("unexpected rejection status: %+v", status)
					}
				}
			}
		})
	}
}
