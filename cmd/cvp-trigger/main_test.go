package main

import (
	"flag"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pkg/errors"
	"github.com/spf13/afero"

	k8sv1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	prowconfig "k8s.io/test-infra/prow/config"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
)

type fakeJobResult struct {
	err error
}

func (v *fakeJobResult) toJSON() ([]byte, error) {
	if v.err != nil {
		return nil, v.err
	}
	return []byte(`null`), v.err
}

func Test_getProwjob(t *testing.T) {
	jobBase := prowconfig.JobBase{Name: "foo"}
	periodic := prowconfig.Periodic{
		JobBase:  jobBase,
		Interval: "",
		Cron:     "",
		Tags:     nil,
	}
	type args struct {
		jobName string
		config  *prowconfig.Config
	}
	tests := []struct {
		name    string
		args    args
		want    *pjapi.ProwJob
		wantErr bool
	}{
		{
			name: "returns prowjob",
			args: args{
				jobName: "foo",
				config: &prowconfig.Config{
					JobConfig: config.JobConfig{
						Presets:           nil,
						PresubmitsStatic:  nil,
						PostsubmitsStatic: nil,
						Periodics:         []prowconfig.Periodic{periodic},
						AllRepos:          nil,
						ProwYAMLGetter:    nil,
					},
					ProwConfig: prowconfig.ProwConfig{},
				},
			},
			want: &pjapi.ProwJob{
				TypeMeta:   v1.TypeMeta{Kind: "ProwJob prow.k8s.io", APIVersion: "v1"},
				ObjectMeta: v1.ObjectMeta{},
				Spec: pjapi.ProwJobSpec{
					Type:   pjapi.PeriodicJob,
					Report: true,
					Job:    "foo",
				},
				Status: pjapi.ProwJobStatus{State: pjapi.TriggeredState},
			},
			wantErr: false,
		},
		{
			name: "returns prowjob in scheduling state",
			args: args{
				jobName: "foo",
				config: &prowconfig.Config{
					JobConfig: config.JobConfig{
						Presets:           nil,
						PresubmitsStatic:  nil,
						PostsubmitsStatic: nil,
						Periodics:         []prowconfig.Periodic{periodic},
						AllRepos:          nil,
						ProwYAMLGetter:    nil,
					},
					ProwConfig: prowconfig.ProwConfig{Scheduler: prowconfig.Scheduler{Enabled: true}},
				},
			},
			want: &pjapi.ProwJob{
				TypeMeta:   v1.TypeMeta{Kind: "ProwJob prow.k8s.io", APIVersion: "v1"},
				ObjectMeta: v1.ObjectMeta{},
				Spec: pjapi.ProwJobSpec{
					Type:   pjapi.PeriodicJob,
					Report: true,
					Job:    "foo",
				},
				Status: pjapi.ProwJobStatus{State: pjapi.SchedulingState},
			},
			wantErr: false,
		},
		{
			name: "returns error",
			args: args{
				jobName: "bar",
				config: &prowconfig.Config{
					JobConfig: config.JobConfig{
						Presets:           nil,
						PresubmitsStatic:  nil,
						PostsubmitsStatic: nil,
						Periodics:         []prowconfig.Periodic{periodic},
						AllRepos:          nil,
						ProwYAMLGetter:    nil,
					},
					ProwConfig: prowconfig.ProwConfig{},
				},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getPeriodicJob(tt.args.jobName, tt.args.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("getPeriodicJob() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			// Do not compare generated fields
			if diff := cmp.Diff(got, tt.want, cmpopts.IgnoreTypes(v1.TypeMeta{}, v1.ObjectMeta{}, v1.Time{})); diff != "" {
				t.Errorf("Unexpected ProwJob: %s", diff)
			}
		})
	}
}

func Test_options_gatherOptions(t *testing.T) {
	fs = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o := options{}
	o.gatherOptions()
	_ = fs.Lookup("channel").Value.Set("foo")
	tests := []struct {
		name string
		want string
	}{
		{
			name: "",
			want: "foo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if fs.Lookup("channel").Value.String() != tt.want {
				t.Errorf("o.gatherOptions() = %s, expected %s", o.channel, tt.want)
			}
		})
	}
}

func Test_options_validate(t *testing.T) {
	fileSystem = afero.NewMemMapFs()
	for _, path := range []string{"/not/empty", "/also/not/empty", "/output/path"} {
		_ = fileSystem.MkdirAll(path, 0755)
	}

	type fields struct {
		bundleImageRef          string
		channel                 string
		indexImageRef           string
		jobConfigPath           string
		jobName                 string
		ocpVersion              string
		outputPath              string
		packageName             string
		prowConfigPath          string
		customScorecardTestcase string
		dryRun                  bool
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name: "valid options",
			fields: fields{
				prowConfigPath:          "/not/empty",
				jobConfigPath:           "/also/not/empty",
				jobName:                 "some-job",
				bundleImageRef:          "master",
				indexImageRef:           "latest",
				ocpVersion:              "4.5",
				outputPath:              "/output/path",
				packageName:             "foo",
				channel:                 "bar",
				customScorecardTestcase: "somescorecard",
				dryRun:                  false,
			},
			wantErr: false,
		},
		{
			name: "missing prow config path",
			fields: fields{
				jobConfigPath:  "/also/not/empty",
				jobName:        "some-job",
				bundleImageRef: "master",
				indexImageRef:  "latest",
				ocpVersion:     "4.5",
				outputPath:     "/output/path",
				packageName:    "foo",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "missing job name",
			fields: fields{
				prowConfigPath: "/not/empty",
				jobConfigPath:  "/also/not/empty",
				bundleImageRef: "master",
				indexImageRef:  "latest",
				ocpVersion:     "4.5",
				outputPath:     "/output/path",
				packageName:    "foo",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "missing bundle image ref",
			fields: fields{
				prowConfigPath: "/not/empty",
				jobConfigPath:  "/also/not/empty",
				jobName:        "some-job",
				indexImageRef:  "latest",
				ocpVersion:     "4.5",
				outputPath:     "/output/path",
				packageName:    "foo",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "missing index image ref",
			fields: fields{
				prowConfigPath: "/not/empty",
				jobConfigPath:  "/also/not/empty",
				jobName:        "some-job",
				bundleImageRef: "master",
				ocpVersion:     "4.5",
				outputPath:     "/output/path",
				packageName:    "foo",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "missing ocp version",
			fields: fields{
				prowConfigPath: "/not/empty",
				jobConfigPath:  "/also/not/empty",
				jobName:        "some-job",
				bundleImageRef: "master",
				indexImageRef:  "latest",
				outputPath:     "/output/path",
				packageName:    "foo",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "invalid ocp version",
			fields: fields{
				prowConfigPath: "/not/empty",
				jobConfigPath:  "/also/not/empty",
				jobName:        "some-job",
				bundleImageRef: "master",
				indexImageRef:  "latest",
				ocpVersion:     "1.5",
				outputPath:     "/output/path",
				packageName:    "foo",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "missing output path version",
			fields: fields{
				prowConfigPath: "/not/empty",
				jobConfigPath:  "/also/not/empty",
				jobName:        "some-job",
				bundleImageRef: "master",
				indexImageRef:  "latest",
				ocpVersion:     "4.5",
				packageName:    "foo",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "missing package name",
			fields: fields{
				prowConfigPath: "/not/empty",
				jobConfigPath:  "/also/not/empty",
				jobName:        "some-job",
				bundleImageRef: "master",
				indexImageRef:  "latest",
				ocpVersion:     "4.5",
				outputPath:     "/output/path",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "missing channel",
			fields: fields{
				prowConfigPath: "/not/empty",
				jobConfigPath:  "/also/not/empty",
				jobName:        "some-job",
				bundleImageRef: "master",
				indexImageRef:  "latest",
				ocpVersion:     "4.5",
				outputPath:     "/output/path",
				packageName:    "foo",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "invalid prow config path",
			fields: fields{
				prowConfigPath: "/invalid/path",
				jobConfigPath:  "/also/not/empty",
				jobName:        "some-job",
				bundleImageRef: "master",
				indexImageRef:  "latest",
				ocpVersion:     "4.5",
				outputPath:     "/output/path",
				packageName:    "foo",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "invalid job config path",
			fields: fields{
				prowConfigPath: "/not/empty",
				jobConfigPath:  "/invalid/path",
				jobName:        "some-job",
				bundleImageRef: "master",
				indexImageRef:  "latest",
				ocpVersion:     "4.5",
				outputPath:     "/output/path",
				packageName:    "foo",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "dry run and no output path",
			fields: fields{
				prowConfigPath: "/not/empty",
				jobConfigPath:  "/also/not/empty",
				jobName:        "some-job",
				bundleImageRef: "master",
				indexImageRef:  "latest",
				ocpVersion:     "4.5",
				packageName:    "foo",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
		{
			name: "dry run and invalid output path",
			fields: fields{
				prowConfigPath: "/not/empty",
				jobConfigPath:  "/also/not/empty",
				jobName:        "some-job",
				bundleImageRef: "master",
				indexImageRef:  "latest",
				ocpVersion:     "4.5",
				outputPath:     "/invalid/path",
				packageName:    "foo",
				channel:        "bar",
				dryRun:         false,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := options{
				bundleImageRef: tt.fields.bundleImageRef,
				channel:        tt.fields.channel,
				indexImageRef:  tt.fields.indexImageRef,
				prowconfig: configflagutil.ConfigOptions{
					ConfigPath:    tt.fields.prowConfigPath,
					JobConfigPath: tt.fields.jobConfigPath,
				},
				jobName:                 tt.fields.jobName,
				ocpVersion:              tt.fields.ocpVersion,
				operatorPackageName:     tt.fields.packageName,
				outputPath:              tt.fields.outputPath,
				customScorecardTestcase: tt.fields.customScorecardTestcase,
				dryRun:                  tt.fields.dryRun,
			}
			if err := o.validateOptions(); (err != nil) != tt.wantErr {
				t.Errorf("validateOptions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_writeResultOutput(t *testing.T) {
	fileSystem = afero.NewMemMapFs()
	afs := afero.Afero{Fs: fileSystem}
	for _, path := range []string{"/path/to/"} {
		_ = afs.MkdirAll(path, 0)
	}
	prowJob := &pjapi.ProwJob{
		Status: pjapi.ProwJobStatus{
			State:   pjapi.SuccessState,
			URL:     "http://example.com/result",
			BuildID: "1",
		},
	}

	prowJobResult := prowjobResult{
		Status:       prowJob.Status.State,
		ArtifactsURL: "",
		URL:          prowJob.Status.URL,
	}

	type args struct {
		prowJobResult prowjobResult
		outputPath    string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "writes file when output path exists and is writable ",
			args: args{
				prowJobResult: prowJobResult,
				outputPath:    "/path/to/outputFile.json",
			},
			wantErr: false,
		},
		{
			name: "writes file when output path doesn't exist, but can be created",
			args: args{
				prowJobResult: prowJobResult,
				outputPath:    "/path/to/outputFile.json",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := writeResultOutput(&tt.args.prowJobResult, tt.args.outputPath); (err != nil) != tt.wantErr {
				t.Errorf("writeResultOutput() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_writeResultOutput_FileSystemFailures(t *testing.T) {
	fileSystem = afero.NewMemMapFs()
	afs := afero.Afero{Fs: fileSystem}
	for _, path := range []string{"/path/to/"} {
		_ = afs.MkdirAll(path, 0)
	}

	// Set our file system to read-only to mimic trying to write to
	// areas without permission
	fileSystem = afero.NewReadOnlyFs(afero.NewMemMapFs())

	type args struct {
		prowjobResult prowjobResult
		outputPath    string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "returns error when output path exists, but isn't writable",
			args: args{
				prowjobResult: prowjobResult{},
				outputPath:    "/path/to/output.json",
			},
			wantErr: true,
		},
		{
			name: "returns error when output path doesn't exist, and cannot be created",
			args: args{
				prowjobResult: prowjobResult{},
				outputPath:    "/some/other/output.json",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := writeResultOutput(&tt.args.prowjobResult, tt.args.outputPath); (err != nil) != tt.wantErr {
				t.Errorf("writeResultOutput() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_writeResultOutput_JsonMarshalFailure(t *testing.T) {
	type args struct {
		prowjobResult jobResult
		outputPath    string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "returns error when unable to marshal prowjobResult struct",
			args: args{
				prowjobResult: &fakeJobResult{err: errors.Errorf("Unable to marshal")},
				outputPath:    "",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := writeResultOutput(tt.args.prowjobResult, tt.args.outputPath); (err != nil) != tt.wantErr {
				t.Errorf("writeResultOutput() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_toJson(t *testing.T) {
	type fields struct {
		Status       pjapi.ProwJobState
		ArtifactsURL string
		URL          string
	}
	tests := []struct {
		name    string
		fields  fields
		want    []byte
		wantErr bool
	}{
		{
			name: "",
			fields: fields{
				Status:       "success",
				ArtifactsURL: "http://example.com/jobName/1/artifacts",
				URL:          "http://example.com/jobName/1/",
			},
			want: []byte(`{
    "status": "success",
    "prowjob_artifacts_url": "http://example.com/jobName/1/artifacts",
    "prowjob_url": "http://example.com/jobName/1/"
}`),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &prowjobResult{
				Status:       tt.fields.Status,
				ArtifactsURL: tt.fields.ArtifactsURL,
				URL:          tt.fields.URL,
			}
			got, err := p.toJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("toJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("toJSON() got = \n%v, want \n%v", string(got), string(tt.want))
			}
		})
	}
}

func Test_getJobArtifactsURL(t *testing.T) {
	org := "redhat-openshift-ecosystem"
	repo := "playground"
	bucket := "test-platform-results"
	browserPrefix := "https://gcsweb-ci.svc.ci.openshift.org/gcs/"
	jobName := "periodic-ci-redhat-openshift-ecosystem-playground-cvp-ocp-4.4-cvp-common-aws"

	prowConfig := &prowconfig.Config{
		JobConfig: prowconfig.JobConfig{},
		ProwConfig: prowconfig.ProwConfig{
			Plank: config.Plank{
				Controller: prowconfig.Controller{},
				DefaultDecorationConfigsMap: map[string]*pjapi.DecorationConfig{
					fmt.Sprintf("%s/%s", org, repo): {GCSConfiguration: &pjapi.GCSConfiguration{Bucket: bucket}},
				},
			},
			Deck: prowconfig.Deck{
				Spyglass: prowconfig.Spyglass{GCSBrowserPrefix: browserPrefix},
			},
		},
	}
	if err := prowConfig.ProwConfig.Plank.FinalizeDefaultDecorationConfigs(); err != nil {
		t.Fatalf("could not finalize config: %v", err)
	}
	type args struct {
		prowJob *pjapi.ProwJob
		config  *prowconfig.Config
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "Returns artifacts URL when we have .Spec.Ref",
			args: args{
				prowJob: &pjapi.ProwJob{
					TypeMeta:   v1.TypeMeta{},
					ObjectMeta: v1.ObjectMeta{Name: "jobWithSpecRef"},
					Spec: pjapi.ProwJobSpec{
						ExtraRefs: nil,
						Job:       jobName,
						Refs:      &pjapi.Refs{Org: org, Repo: repo},
						Type:      "periodic",
					},
					Status: pjapi.ProwJobStatus{State: "success", BuildID: "100"},
				},
				config: prowConfig,
			},
			want: "https://gcsweb-ci.svc.ci.openshift.org/gcs/test-platform-results/logs/periodic-ci-redhat-openshift-ecosystem-playground-cvp-ocp-4.4-cvp-common-aws/100",
		},
		{
			name: "Returns artifacts URL when we have Spec.ExtraRefs",
			args: args{
				prowJob: &pjapi.ProwJob{
					TypeMeta:   v1.TypeMeta{},
					ObjectMeta: v1.ObjectMeta{Name: "jobWithExtraRef"},
					Spec: pjapi.ProwJobSpec{
						ExtraRefs: []pjapi.Refs{
							{Org: org, Repo: repo},
							{Org: "org2", Repo: "repo2"},
						},
						Job:  jobName,
						Refs: nil,
						Type: "periodic",
					},
					Status: pjapi.ProwJobStatus{State: "success", BuildID: "101"},
				},
				config: prowConfig,
			},
			want: "https://gcsweb-ci.svc.ci.openshift.org/gcs/test-platform-results/logs/periodic-ci-redhat-openshift-ecosystem-playground-cvp-ocp-4.4-cvp-common-aws/101",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getJobArtifactsURL(tt.args.prowJob, tt.args.config); got != tt.want {
				t.Errorf("getJobArtifactsURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppendMultiStageParams(t *testing.T) {
	multiStageParamString := "--multi-stage-param=%s=%s"
	tests := []struct {
		name         string
		params       map[string]string
		expectedArgs []string
	}{
		{
			name: "Multi stage params are added",
			params: map[string]string{
				BundleImage: "bundle",
				Channel:     "channel",
				IndexImage:  "index",
				Package:     "package",
			},
			expectedArgs: []string{
				fmt.Sprintf(multiStageParamString, BundleImage, "bundle"),
				fmt.Sprintf(multiStageParamString, Channel, "channel"),
				fmt.Sprintf(multiStageParamString, IndexImage, "index"),
				fmt.Sprintf(multiStageParamString, Package, "package"),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := k8sv1.PodSpec{
				Containers: []k8sv1.Container{{}},
			}
			appendMultiStageParams(&spec, tc.params)
			if diff := cmp.Diff(tc.expectedArgs, spec.Containers[0].Args); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
		})
	}
}
