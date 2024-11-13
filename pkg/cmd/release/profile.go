package release

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

type balanceProfileOptions struct {
	*options
	SrcProfile        string
	DstProfiles       []string
	ScalingFactor     float64
	BalancingFactors  []float64
	BalancingStrategy string
	ExcludeOrgs       []string
}

func (o *balanceProfileOptions) validate() error {
	if o.ScalingFactor < 0.0 || o.ScalingFactor > 1.0 {
		return fmt.Errorf("scaling factor not in range [0, 1]")
	}

	if len(o.BalancingFactors) == 0 {
		o.BalancingFactors = make([]float64, len(o.DstProfiles))
		f := 1.0 / float64(len(o.DstProfiles))
		for i := range len(o.DstProfiles) {
			o.BalancingFactors[i] = f
		}
	} else {
		if len(o.BalancingFactors) != len(o.DstProfiles) {
			return fmt.Errorf("balancing factors must match target profiles")
		}
		tot := 0.0
		for _, f := range o.BalancingFactors {
			if f < 0.0 || f > 1.0 {
				return fmt.Errorf("balancing factor %f not in range [0, 1]", f)
			}
			tot += f
		}
		if math.Abs(1.0-tot) > 0.1 {
			return fmt.Errorf("balancing factors must sum up to 1.0")
		}
	}

	return nil
}

type testInfo struct {
	test   *api.TestStepConfiguration
	config *api.ReleaseBuildConfiguration
	info   *config.Info
}

type dstProfileInfo struct {
	Name string
	// How many test are going to be assigned to this profile
	Target int
	// Tests that have been assigned already
	N int
}

func newProfileCommand(o *options) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "cluster profile commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	listCmd := newProfileListCommand()
	reshuffleCmd, err := newBalanceProfileCommand(o)
	if err != nil {
		return nil, fmt.Errorf("balance cmd: %w", err)
	}
	cmd.AddCommand(listCmd, reshuffleCmd)
	return cmd, nil
}

func newProfileListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list cluster profiles",
		RunE: func(_ *cobra.Command, args []string) error {
			return cmdProfileList(args)
		},
	}
}

func cmdProfileList(args []string) error {
	if len(args) == 0 {
		for _, p := range api.ClusterProfiles() {
			args = append(args, string(p))
		}
	} else {
		valid := sets.New[string]()
		for _, p := range api.ClusterProfiles() {
			valid.Insert(string(p))
		}
		for _, arg := range args {
			if !valid.Has(arg) {
				return fmt.Errorf("invalid cluster profile: %s", arg)
			}
		}
	}
	return profilePrint(args)
}

func profilePrint(args []string) error {
	type P struct {
		Profile     api.ClusterProfile `json:"profile"`
		ClusterType string             `json:"cluster_type"`
		LeaseType   string             `json:"lease_type"`
		Secret      string             `json:"secret"`
		ConfigMap   string             `json:"config_map,omitempty"`
	}
	var l []P
	for _, arg := range args {
		p := api.ClusterProfile(arg)
		l = append(l, P{
			Profile:     p,
			ClusterType: p.ClusterType(),
			LeaseType:   p.LeaseType(),
		})
	}
	return printYAML(l)
}

// The following incantation:
//
//	--from=gcp --scaling-factor=0.5 --to=gcp2,gcp3 --balancing-factors=0.75,0.25 --exclude-orgs=openshift-priv
//
// gather every ci-operator config, excluding the ones under openshift-priv/, having at least
// one test that has `cluster_profile: gcp` set.
// Assuming the selected tests to be N = 100, it then redistributes R = N*0.5 = 50 of them.
//
// The balancing algorithm follows this schema:
//
//	tests that will target 'gcp2': R * 0.75 = 37 => adjusted to 38 to compensate truncation errors
//	tests that will target 'gcp3': R * 0.25 = 12
//
// After the distribution has taken place:
//
//	'gcp'  tests: 50
//	'gcp2' tests: +38
//	'gcp3' tests: +12
func newBalanceProfileCommand(parent *options) (*cobra.Command, error) {
	o := balanceProfileOptions{options: parent}
	cmd := &cobra.Command{
		Use:   "balance",
		Short: "balance profiles",
		Long:  "balance profiles",
		RunE: func(_ *cobra.Command, args []string) error {
			if err := o.validate(); err != nil {
				return err
			}
			return balanceProfiles(&o)
		},
	}
	flags := cmd.PersistentFlags()
	flags.StringVar(&o.SrcProfile, "from", "", "Cluster profile we want offload jobs from")
	flags.StringSliceVar(&o.DstProfiles, "to", nil, "Cluster profiles we want assign jobs to")
	flags.Float64VarP(&o.ScalingFactor, "scaling-factor", "s", 0.25, "Scaling factor in % for source profile")
	flags.Float64SliceVarP(&o.BalancingFactors, "balancing-factors", "b", []float64{}, "Balancing factor in % for each target profile")
	flags.StringVarP(&o.BalancingStrategy, "strategy", "x", "rand", "Balancing algorithm")
	flags.StringSliceVar(&o.ExcludeOrgs, "exclude-orgs", []string{}, "Skip configs under ci-operator/config/${ORG}")
	if err := cmd.MarkPersistentFlagRequired("from"); err != nil {
		return nil, err
	}
	if err := cmd.MarkPersistentFlagRequired("to"); err != nil {
		return nil, err
	}
	return cmd, nil
}

func gatherTests(configsPath, srcProfile string, excludeOrgs []string) ([]testInfo, error) {
	tests := make([]testInfo, 0)
	excludeOrgsSet := sets.New(excludeOrgs...)
	if err := config.OperateOnCIOperatorConfigDir(configsPath, func(c *api.ReleaseBuildConfiguration, info *config.Info) error {
		if excludeOrgsSet.Has(info.Org) {
			return nil
		}
		for i := range c.Tests {
			test := &c.Tests[i]
			if test.MultiStageTestConfiguration != nil {
				if test.MultiStageTestConfiguration.ClusterProfile == api.ClusterProfile(srcProfile) {
					tests = append(tests, testInfo{test: test, config: c, info: info})
				}
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("scan ci-operator configs %s: %w", configsPath, err)
	}
	return tests, nil
}

func balanceRandomly(tests []testInfo, testsToBalance int, dstProfilesInfo []dstProfileInfo) []config.DataWithInfo {
	modifiedConfigs := sets.New[string]()
	configsToCommit := make([]config.DataWithInfo, 0, testsToBalance)
	idxGenerator := rand.New(rand.NewSource(time.Now().Unix()))
	profileIdx := 0
	duplicatedIdx := sets.New[int]()
	for range testsToBalance {
		var profile api.ClusterProfile
		for {
			p := &dstProfilesInfo[profileIdx]
			if p.N+1 > p.Target {
				profileIdx = (profileIdx + 1) % len(dstProfilesInfo)
			} else {
				profile = api.ClusterProfile(p.Name)
				p.N += 1
				break
			}
		}

		var i int
		for {
			i = idxGenerator.Intn(len(tests))
			if !duplicatedIdx.Has(i) {
				duplicatedIdx.Insert(i)
				break
			}
		}

		t := tests[i]
		t.test.MultiStageTestConfiguration.ClusterProfile = profile

		configName := t.info.AsString()
		if !modifiedConfigs.Has(configName) {
			configsToCommit = append(configsToCommit, config.DataWithInfo{Configuration: *t.config, Info: *t.info})
			modifiedConfigs.Insert(configName)
		}

		profileIdx = (profileIdx + 1) % len(dstProfilesInfo)
	}

	return configsToCommit
}

func dstProfilesInfo(dstProfiles []string, factors []float64, testsToBalance int) []dstProfileInfo {
	dstProfilesInfo := make([]dstProfileInfo, 0, len(factors))
	total := 0
	for i := range len(factors) {
		target := int(math.Trunc(float64(testsToBalance) * factors[i]))
		total += target
		dstProfilesInfo = append(dstProfilesInfo, dstProfileInfo{Name: dstProfiles[i], N: 0, Target: target})
	}

	// Fix truncation error
	i := 0
	for range testsToBalance - total {
		dstProfilesInfo[i].Target += 1
		i = (i + 1) & len(dstProfilesInfo)
	}

	return dstProfilesInfo
}

func balanceProfiles(o *balanceProfileOptions) error {
	configsPath := o.argsWithPrefixes(config.CiopConfigInRepoPath, o.ciOperatorConfigPath, nil)[0]
	tests, err := gatherTests(configsPath, o.SrcProfile, o.ExcludeOrgs)
	if err != nil {
		return err
	}

	testsToBalance := int(math.Trunc(float64(len(tests)) * o.ScalingFactor))
	dstProfilesInfo := dstProfilesInfo(o.DstProfiles, o.BalancingFactors, testsToBalance)

	fmt.Printf("targeted tests: %d\ntests to balance: %d\nprofiles:\n", len(tests), testsToBalance)
	for _, dstProfileInfo := range dstProfilesInfo {
		fmt.Printf("  %s: %d\n", dstProfileInfo.Name, dstProfileInfo.Target)
	}

	var configsToCommit []config.DataWithInfo
	switch o.BalancingStrategy {
	case "rand":
		configsToCommit = balanceRandomly(tests, testsToBalance, dstProfilesInfo)
	default:
		return fmt.Errorf("balancing strategy %s not supported", o.BalancingStrategy)
	}

	for i := range configsToCommit {
		c := &configsToCommit[i]
		if err := c.CommitTo(configsPath); err != nil {
			return fmt.Errorf("commit config %s: %w", c.Info.AsString(), err)
		}
	}

	return nil
}
