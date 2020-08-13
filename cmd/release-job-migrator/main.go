package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	prowconfig "k8s.io/test-infra/prow/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

type options struct {
	releaseRepoDir string

	determinize  bool
	mutate       bool
	dereference  bool
	annotate     bool
	clean        bool
	detrivialize bool

	logLevel string
}

func (o *options) Validate() error {
	if o.releaseRepoDir == "" {
		return errors.New("required flag --release-repo-dir was unset")
	}

	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	logrus.SetLevel(level)
	return nil
}

func (o *options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.releaseRepoDir, "release-repo-dir", "", "Path to openshift/release repo.")
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.BoolVar(&o.determinize, "determinize", false, "Determinize config spec")
	fs.BoolVar(&o.mutate, "mutate", false, "Make nontrivial changes to scripts and config")
	fs.BoolVar(&o.dereference, "dereference", false, "Dereference environment variables")
	fs.BoolVar(&o.annotate, "annotate", false, "Change to using in-template annotation")
	fs.BoolVar(&o.clean, "clean", false, "Remove empty lines from scripts")
	fs.BoolVar(&o.detrivialize, "detrivialize", false, "Simplify trivial scripts")
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o.Bind(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

const (
	first    = `initial=$(ARTIFACTS)/release-initial.json`
	last     = `export RELEASE_IMAGE_LATEST=$( python -c 'import json,sys; print json.load(open(sys.argv[1], "r"))["pullSpec"]' "${latest}" )`
	comment  = `# prow doesn't allow init containers or a second container`
	annotate = `oc --kubeconfig /etc/appci/sa.release-bot.app.ci.config annotate pj/${PROW_JOB_ID}`
)

var (
	releaseMatcher  = regexp.MustCompile(`https://([^/]+)/api/v1/releasestream/([^/]+)/latest( --data-urlencode '([^']+)')? > \${([^}]+)}`)
	boundsMatcher   = regexp.MustCompile(`in=>(.+) <(.*)`)
	relativeMatcher = regexp.MustCompile(`rel=([0-9]+)`)
	trivial         = []string{
		"/bin/bash",
		"-c",
		`#!/bin/bash
set -euo pipefail
ci-operator $@
`,
		"",
	}
)

func digestRelease(line string) (string, api.UnresolvedRelease, bool) {
	parts := releaseMatcher.FindStringSubmatch(line)
	switch len(parts) {
	case 6:
		// technically parts[1] contains the product and arch but they're all OCP/amd64 so who cares
		switch parts[2] {
		case "4-stable":
			bounds := boundsMatcher.FindStringSubmatch(parts[4])
			if len(bounds) != 3 {
				fmt.Printf("expected line to contain bounds: %v\n", line)
				return "", api.UnresolvedRelease{}, false
			}
			return parts[5], api.UnresolvedRelease{
				Prerelease: &api.Prerelease{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureAMD64,
					VersionBounds: api.VersionBounds{
						Lower: bounds[1],
						Upper: bounds[2],
					},
				},
			}, true
		default:
			stream := api.ReleaseStreamNightly
			if strings.Contains(parts[2], "ci") {
				stream = api.ReleaseStreamCI
			}
			release := api.UnresolvedRelease{
				Candidate: &api.Candidate{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureAMD64,
					Stream:       stream,
					Version:      parts[2][:3],
				},
			}
			if parts[4] != "" {
				relative := relativeMatcher.FindStringSubmatch(parts[4])
				if len(relative) != 2 {
					fmt.Printf("expected line to contain relative: %v\n", line)
					return "", api.UnresolvedRelease{}, false
				}
				relInt, err := strconv.Atoi(relative[1])
				if err != nil {
					fmt.Printf("could not parse relative: %v\n", err)
					return "", api.UnresolvedRelease{}, false
				}
				release.Candidate.Relative = relInt
			}
			return parts[5], release, true
		}
	default:
		return "", api.UnresolvedRelease{}, false
	}
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	if err := jobconfig.OperateOnJobConfigDir(path.Join(o.releaseRepoDir, config.JobConfigInRepoPath), func(jobConfig *prowconfig.JobConfig, info *jobconfig.Info) error {
		for i := range jobConfig.Periodics {
			if jobConfig.Periodics[i].JobBase.Spec == nil || jobConfig.Periodics[i].JobBase.Spec.Containers == nil || len(jobConfig.Periodics[i].JobBase.Spec.Containers[0].Command) < 3 {
				continue
			}
			releases := map[string]api.UnresolvedRelease{}
			if o.mutate {
				script := jobConfig.Periodics[i].JobBase.Spec.Containers[0].Command[2]
				if !strings.Contains(script, first) {
					continue
				}
				lines := strings.Split(script, "\n")
				var start, end int
				for j, line := range lines {
					if strings.Contains(line, first) {
						start = j
					} else if strings.Contains(line, last) {
						end = j
					}
					if name, release, ok := digestRelease(line); ok {
						releases[name] = release
					}
				}
				edited := strings.Join(append(lines[:start], lines[end+1:]...), `\n`)
				jobConfig.Periodics[i].JobBase.Spec.Containers[0].Command[2] = edited
			}

			if o.dereference {
				script := jobConfig.Periodics[i].JobBase.Spec.Containers[0].Command[2]
				if !strings.Contains(script, first) {
					continue
				}
				container := jobConfig.Periodics[i].JobBase.Spec.Containers[0]
				toReplace := map[string]string{}
				for j := range container.Env {
					toReplace[container.Env[j].Name] = container.Env[j].Value
				}
				replaceAll := func(in string) string {
					for from, to := range toReplace {
						in = strings.ReplaceAll(in, fmt.Sprintf("$(%s)", from), to)
					}
					return in
				}
				for j := range container.Args {
					jobConfig.Periodics[i].JobBase.Spec.Containers[0].Args[j] = replaceAll(container.Args[j])
				}
				for j := range container.Command {
					jobConfig.Periodics[i].JobBase.Spec.Containers[0].Command[j] = replaceAll(container.Command[j])
				}
				for j := range container.Env {
					jobConfig.Periodics[i].JobBase.Spec.Containers[0].Env[j].Value = replaceAll(container.Env[j].Value)
				}
			}

			for j, item := range jobConfig.Periodics[i].JobBase.Spec.Containers[0].Env {
				if item.Name == "CONFIG_SPEC" {
					configSpec := api.ReleaseBuildConfiguration{}
					if err := yaml.UnmarshalStrict([]byte(item.Value), &configSpec); err != nil {
						logrus.WithError(err).Fatal("Could not read config spec env var")
					}
					if o.mutate {
						configSpec.InputConfiguration.ReleaseTagConfiguration = nil
						configSpec.InputConfiguration.Releases = releases
					}

					raw, err := yaml.Marshal(configSpec)
					if err != nil {
						logrus.WithError(err).Fatal("Could not serialize config spec env var")
					}

					if o.determinize || o.mutate {
						jobConfig.Periodics[i].JobBase.Spec.Containers[0].Env[j].Value = string(raw)
					}
				}
			}

			if o.annotate {
				container := jobConfig.Periodics[i].JobBase.Spec.Containers[0]
				script := container.Command[2]
				if !strings.Contains(script, annotate) {
					continue
				}
				lines := strings.Split(script, "\n")
				var start, end int
				for j, line := range lines {
					if strings.Contains(line, comment) {
						start = j
					} else if strings.Contains(line, annotate) {
						end = j
					}
				}
				edited := strings.Join(append(lines[:start], lines[end+1:]...), `\n`)
				jobConfig.Periodics[i].JobBase.Spec.Containers[0].Command[2] = edited
				jobConfig.Periodics[i].JobBase.Spec.Containers[0].Args = append(container.Args, "--secret-dir=/etc/appci")
			}

			if o.clean && strings.Contains(jobConfig.Periodics[i].JobBase.Name, "upgrade") {
				container := jobConfig.Periodics[i].JobBase.Spec.Containers[0]
				script := container.Command[2]
				var edited []string
				lines := strings.Split(script, "\n")
				for j := range lines {
					if strings.TrimSpace(lines[j]) != "" {
						edited = append(edited, lines[j])
					}
				}
				jobConfig.Periodics[i].JobBase.Spec.Containers[0].Command[2] = strings.Join(edited, `\n`)
			}

			if o.detrivialize {
				if reflect.DeepEqual(jobConfig.Periodics[i].JobBase.Spec.Containers[0].Command, trivial) {
					jobConfig.Periodics[i].JobBase.Spec.Containers[0].Command = []string{"ci-operator"}
				}
			}
		}
		return jobconfig.WriteToFile(info.Filename, jobConfig)
	}); err != nil {
		logrus.WithError(err).Fatal("Could not load Prow job configurations.")
	}
}

// to get rid of multiline BS:
//#!/bin/bash
//
//set -o errexit
//
//for file in ci-operator/jobs/openshift/release/openshift-release-release-4.*-periodics.yaml; do
//sed -i -e "s/- '#/- |\n        #/g" -e "s/n'$/n/g" -e "s/@'$/@/g" -e "s/''/'/g" $file
//sed -i 's/\\n/\n        /g' $file
//done
