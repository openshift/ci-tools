package main

import (
	"flag"
	"os"
	"testing"

	"github.com/openshift/ci-tools/pkg/prowjob"
	"github.com/spf13/afero"
)

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
		bundleImageRef string
		channel        string
		indexImageRef  string
		jobConfigPath  string
		jobName        string
		ocpVersion     string
		outputPath     string
		packageName    string
		prowConfigPath string
		dryRun         bool
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name: "valid options",
			fields: fields{
				prowConfigPath: "/not/empty",
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
			name: "missing job config path",
			fields: fields{
				prowConfigPath: "/not/empty",
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
			wantErr: false,
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
			pjo := prowjob.ProwJobOptions{
				JobConfigPath:  tt.fields.jobConfigPath,
				JobName:        tt.fields.jobName,
				ProwConfigPath: tt.fields.prowConfigPath,
				OutputPath:     tt.fields.outputPath,
				DryRun:         tt.fields.dryRun,
			}
			o := options{
				bundleImageRef:      tt.fields.bundleImageRef,
				channel:             tt.fields.channel,
				indexImageRef:       tt.fields.indexImageRef,
				ocpVersion:          tt.fields.ocpVersion,
				operatorPackageName: tt.fields.packageName,
				ProwJobOptions:      pjo,
			}
			if err := o.validateOptions(fileSystem); (err != nil) != tt.wantErr {
				t.Errorf("validateOptions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
