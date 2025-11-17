package main

import (
	"flag"
	"math"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config/secret"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/dispatcher"
)

type ProfilesFlag [][]string

type options struct {
	profiles             ProfilesFlag
	prometheusDaysBefore int
	dispatcher.PrometheusOptions
}

func (p *ProfilesFlag) Set(val string) error {
	parts := strings.Split(val, ",")
	*p = append(*p, parts)
	return nil
}

func (p *ProfilesFlag) String() string {
	var groups []string
	for _, grp := range *p {
		groups = append(groups, strings.Join(grp, ","))
	}
	return strings.Join(groups, ";")
}

func main() {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.Var(&o.profiles, "profiles", "Comma-separated list of profiles; may be repeated")
	fs.IntVar(&o.prometheusDaysBefore, "prometheus-days-before", 14,
		"Number [1,15] of days before. Time 00-00-00 of that day will be used as time to query Prometheus. E.g., 1 means 00-00-00 of yesterday.")
	o.PrometheusOptions.AddFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}

	if o.PrometheusOptions.PrometheusPasswordPath != "" {
		if err := secret.Add(o.PrometheusOptions.PrometheusPasswordPath); err != nil {
			logrus.WithError(err).Fatal("Failed to start secrets agent")
		}
	}

	if o.PrometheusOptions.PrometheusBearerTokenPath != "" {
		if err := secret.Add(o.PrometheusOptions.PrometheusBearerTokenPath); err != nil {
			logrus.WithError(err).Fatal("Failed to start secrets agent")
		}
	}
	promVolumes, err := dispatcher.NewPrometheusVolumes(o.PrometheusOptions, o.prometheusDaysBefore)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create prometheus volumes")
	}

	vol, err := promVolumes.GetJobVolumes()
	if err != nil {
		logrus.WithError(err).Fatal("failed to fetch prometheus volumes")
	}

	buckets := make(map[api.ClusterProfile]float64)
	membership := make(map[api.ClusterProfile][]api.ClusterProfile)
	for _, group := range o.profiles {
		var profList []api.ClusterProfile
		for _, p := range group {
			prof := api.ClusterProfile(p)
			if _, exists := buckets[prof]; !exists {
				buckets[prof] = 0
			}
			profList = append(profList, prof)
		}
		for _, prof := range profList {
			membership[prof] = profList
		}
	}

	configsPath := config.CiopConfigInRepoPath
	configs := make([]config.DataWithInfo, 0)

	err = config.OperateOnCIOperatorConfigDir(configsPath, func(
		c *api.ReleaseBuildConfiguration,
		info *config.Info,
	) error {
		for i := range c.Tests {
			t := &c.Tests[i]
			ms := t.MultiStageTestConfiguration
			if ms == nil {
				continue
			}

			current := ms.ClusterProfile
			group, ok := membership[current]
			if !ok || len(group) == 0 {
				continue
			}

			var bestProf api.ClusterProfile
			minVal := math.MaxFloat64
			for _, prof := range group {
				if val := buckets[prof]; val < minVal {
					minVal = val
					bestProf = prof
				}
			}
			if ms.ClusterProfile != bestProf {
				logrus.Infof("reassigning test %q: %s -> %s", t.As, ms.ClusterProfile, bestProf)
				ms.ClusterProfile = bestProf
			}

			weight := vol[getTestName(t, info)]
			if weight == 0 {
				continue
			}
			buckets[bestProf] += weight
			configs = append(configs, config.DataWithInfo{Configuration: *c, Info: *info})
		}
		return nil
	})
	if err != nil {
		logrus.WithError(err).Fatal("error distributing tests across profiles")
	}

	for i := range configs {
		c := &configs[i]
		if err := c.CommitTo(configsPath); err != nil {
			logrus.WithError(err).Fatal("commit config")
		}
	}

	for prof, val := range buckets {
		logrus.WithField("weight", val).WithField("profile", prof).Info("Calculated weight")
	}
}

func getTestName(t *api.TestStepConfiguration, info *config.Info) string {
	test := ""
	if t.IsPeriodic() {
		test += "periodic-ci-"
	} else if t.Postsubmit {
		test += "branch-ci-"
	} else {
		test += "pull-ci-"
	}

	if info.Variant != "" {
		test += info.Org + "-" + info.Repo + "-" + info.Branch + "-" + info.Variant + "-" + t.As
	} else {
		test += info.Org + "-" + info.Repo + "-" + info.Branch + "-" + t.As

	}
	return test
}
