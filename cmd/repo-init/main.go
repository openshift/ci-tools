// repo-init is an interactive command-line utility to bootstrap
// configuration options for repositories, including config for
// prow as well as ci-operator. It is not intended to replace
// manual interaction with the configuration, especially for all
// complicated scenarios, but to provide a good set of defaults.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/plugins"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	ciopconfig "github.com/openshift/ci-tools/pkg/config"
)

type options struct {
	releaseRepo string
	config      string
}

func (o *options) Validate() error {
	if o.releaseRepo == "" {
		return errors.New("--release-repo is required")
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.releaseRepo, "release-repo", "", "Path to the root of the openshift/release repository.")
	fs.StringVar(&o.config, "config", "", "JSON configuration to use instead of the interactive mode.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Printf("ERROR: could not parse input: %v", err)
		os.Exit(1)
	}
	return o
}

type initConfig struct {
	Org                   string    `json:"org"`
	Repo                  string    `json:"repo"`
	Branch                string    `json:"branch"`
	CanonicalGoRepository string    `json:"canonical_go_repository"`
	Promotes              bool      `json:"promotes"`
	PromotesWithOpenShift bool      `json:"promotes_with_openshift"`
	NeedsBase             bool      `json:"needs_base"`
	NeedsOS               bool      `json:"needs_os"`
	GoVersion             string    `json:"go_version"`
	BuildCommands         string    `json:"build_commands"`
	TestBuildCommands     string    `json:"test_build_commands"`
	Tests                 []test    `json:"tests"`
	CustomE2E             []e2eTest `json:"custom_e2e"`
	ReleaseType           string    `json:"release_type"`
	ReleaseVersion        string    `json:"release_version"`
}

type test struct {
	As      string                              `json:"as"`
	From    api.PipelineImageStreamTagReference `json:"from"`
	Command string                              `json:"command"`
}

type e2eTest struct {
	As      string             `json:"as"`
	Profile api.ClusterProfile `json:"profile"`
	Command string             `json:"command"`
	Cli     bool               `json:"cli"`
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		errorExit(fmt.Sprintf("invalid options: %v", err))
	}

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	fmt.Println(`Welcome to the repository configuration initializer.
In order to generate a new set of configurations, some information will be necessary.`)
	var config initConfig
	if o.config != "" {
		fmt.Println("Loading configuration from flags ...")
		if err := json.Unmarshal([]byte(o.config), &config); err != nil {
			errorExit(fmt.Sprintf("could not unmarshal provided configuration: %v", err))
		}
	} else {
		fmt.Println(`
Let's start with general information about the repository...`)
		config.Org = fetchWithPrompt("Enter the organization for the repository:")
		config.Repo = fetchWithPrompt("Enter the repository to initialize:")
		config.Branch = fetchOrDefaultWithPrompt("Enter the development branch for the repository:", "master")

		configPath := path.Join(o.releaseRepo, "ci-operator", "config", config.Org, config.Repo)
		if _, err := os.Stat(configPath); err == nil {
			errorExit(fmt.Sprintf("configuration for %s/%s already exists at %s", config.Org, config.Repo, configPath))
		}

		fmt.Println(`
Now, let's determine how the repository builds output artifacts...`)
		config.Promotes = fetchBoolWithPrompt("Does the repository build and promote container images? ")
		if config.Promotes {
			config.PromotesWithOpenShift = fetchBoolWithPrompt("Does the repository promote images as part of the OpenShift release? ")
			config.NeedsBase = fetchBoolWithPrompt("Do any images build on top of the OpenShift base image? ")
			config.NeedsOS = fetchBoolWithPrompt("Do any images build on top of the CentOS base image? ")
		}

		fmt.Println(`
Now, let's configure how the repository is compiled...`)
		config.GoVersion = fetchOrDefaultWithPrompt("What version of Go does the repository build with?", "1.13")
		config.CanonicalGoRepository = fetchOrDefaultWithPrompt("[OPTIONAL] Enter the Go import path for the repository if it uses a vanity URL (e.g. \"k8s.io/my-repo\"):", "")
		config.BuildCommands = fetchOrDefaultWithPrompt("[OPTIONAL] What commands are used to build binaries in the repository? (e.g. \"go install ./cmd/...\")", "")
		config.TestBuildCommands = fetchOrDefaultWithPrompt("[OPTIONAL] What commands are used to build test binaries? (e.g. \"go install -race ./cmd/...\" or \"go test -c ./test/...\")", "")

		fmt.Println(`
Now, let's configure test jobs for the repository...`)
		names := sets.NewString()
		var tests []test
		for {
			more := ""
			detail := `
First, we will configure simple test scripts. Test scripts
execute unit or integration style tests by running a command
from your repository inside of a test container. For example,
a unit test may be executed by running "make test-unit" after
checking out the code under test.

`
			if len(tests) > 0 {
				more = "more "
				detail = ""
			}
			if !fetchBoolWithPrompt(fmt.Sprintf("%sAre there any %stest scripts to configure? ", detail, more)) {
				break
			}
			var test test
			test.As = fetchWithPrompt("What is the name of this test (e.g. \"unit\")? ")
			for {
				if names.Has(test.As) {
					fmt.Printf(`
A test named %s already exists. Please choose a different name.\n`, test.As)
					test.As = fetchWithPrompt("What is the name of this test (e.g. \"unit\")? ")
				} else {
					names.Insert(test.As)
					break
				}
			}
			var usesBinaries, usesTestBinaries bool
			if config.BuildCommands != "" {
				usesBinaries = fetchBoolWithPrompt("Does this test require built binaries? ")
			}
			if config.TestBuildCommands != "" && !usesBinaries {
				usesTestBinaries = fetchBoolWithPrompt("Does this test require test binaries? ")
			}
			switch {
			case !usesBinaries && !usesTestBinaries:
				test.From = api.PipelineImageStreamTagReferenceSource
			case usesBinaries:
				test.From = api.PipelineImageStreamTagReferenceBinaries
			case usesTestBinaries:
				test.From = api.PipelineImageStreamTagReferenceTestBinaries
			}
			test.Command = fetchWithPrompt("What commands in the repository run the test (e.g. \"make test-unit\")? ")
			tests = append(tests, test)
		}
		config.Tests = tests

		var e2eTests []e2eTest
		for {
			more := ""
			detail := `
Next, we will configure end-to-end tests. An end-to-end test
executes a command from your repository against an ephemeral
OpenShift cluster. The test script will have "cluster:admin"
credentials with which it can execute no other tests will
share the cluster.

`
			if len(e2eTests) > 0 {
				more = "more "
				detail = ""
			}
			if !fetchBoolWithPrompt(fmt.Sprintf("%sAre there any %send-to-end test scripts to configure? ", detail, more)) {
				break
			}
			var test e2eTest
			test.As = fetchWithPrompt("What is the name of this test (e.g. \"e2e-operator\")? ")
			for {
				if names.Has(test.As) {
					fmt.Printf(`
A test named %s already exists. Please choose a different name.\n`, test.As)
					test.As = fetchWithPrompt("What is the name of this test (e.g. \"e2e-operator\")? ")
				} else {
					names.Insert(test.As)
					break
				}
			}

			clusterProfiles := sets.NewString("gcp", "aws", "azure")
			test.Profile = api.ClusterProfile(fetchOrDefaultWithPrompt("Which specific cloud provider does the test require, if any? ", string(api.ClusterProfileAWS)))
			for {
				if !clusterProfiles.Has(string(test.Profile)) {
					fmt.Printf("Cluster profile %s is not valid. Please choose one from: %s.\n", test.Profile, strings.Join(clusterProfiles.List(), ", "))
					test.Profile = api.ClusterProfile(fetchOrDefaultWithPrompt("Which specific cloud provider does the test require, if any? ", string(api.ClusterProfileAWS)))
				} else {
					break
				}
			}
			test.Command = fetchWithPrompt("What commands in the repository run the test (e.g. \"make test-e2e\")? ")
			test.Cli = fetchBoolWithPrompt("Does your test require the OpenShift client (oc)? ")

			e2eTests = append(e2eTests, test)
		}

		config.CustomE2E = e2eTests
		if len(config.CustomE2E) > 0 && !config.Promotes {
			valid := sets.NewString("nightly", "published")
			validFormatted := strings.Join(valid.List(), ", ")
			releaseType := fetchWithPrompt(fmt.Sprintf("What type of OpenShift release do the end-to-end tests run on top of? [%s]", validFormatted))
			for {
				if !valid.Has(releaseType) {
					fmt.Printf(`
Unexpected release type %q. Please choose one from: [%v].\n`, releaseType, validFormatted)
					releaseType = fetchWithPrompt(fmt.Sprintf("What type of OpenShift release do the end-to-end tests run on top of? [%s]", validFormatted))
				} else {
					break
				}
			}
			config.ReleaseType = releaseType
			config.ReleaseVersion = fetchOrDefaultWithPrompt("Which OpenShift version is being tested? ", "4.6")
		}
	}

	marshalled, err := json.Marshal(&config)
	if err != nil {
		errorExit(fmt.Sprintf("could not marshal configuration: %v", err))
	}
	fmt.Printf(`
Repository configuration options loaded!
In case of any errors, use the following command to re-
create this run without using the interactive interface:

%s --config=%q
`, strings.Join(os.Args, " "), string(marshalled))

	if err := updateProwConfig(config, o.releaseRepo); err != nil {
		errorExit(fmt.Sprintf("could not update Prow configuration: %v", err))
	}

	if err := updatePluginConfig(config, o.releaseRepo); err != nil {
		errorExit(fmt.Sprintf("could not update Prow plugin configuration: %v", err))
	}

	if err := createCIOperatorConfig(config, o.releaseRepo); err != nil {
		errorExit(fmt.Sprintf("could not generate new CI Operator configuration: %v", err))
	}
}

func errorExit(msg string) {
	fmt.Printf("ERROR: %s\n", msg)
	os.Exit(1)
}

func errorRetry(msg string) {
	fmt.Printf("ERROR: %s\nPlease try again.\n", msg)
}

func fetchBoolWithPrompt(msg string) bool {
	response := errorRetry
	for i := 0; i < 5; i++ {
		if i == 4 {
			response = errorExit
		}
		out := fetchOrDefaultWithPrompt(msg, "no")
		switch out {
		case "t", "T", "true", "y", "Y", "yes", "Yes", "YES":
			return true
		case "f", "F", "false", "n", "N", "no", "No", "NO":
			return false
		default:
			response(fmt.Sprintf("%q is not recognized, please respond \"yes\" or \"no\"", out))
			continue
		}
	}
	// dead code below
	return false
}

func fetchWithPrompt(msg string) string {
	response := errorRetry
	for i := 0; i < 5; i++ {
		if i == 4 {
			response = errorExit
		}
		out := fetchOrDefaultWithPrompt(msg, "")
		if out == "" {
			response("a response is required")
			continue
		}
		return out
	}
	// dead code below
	return ""
}

// creating a reader from stdin consumes all of the content from the pipe,
// so a shared reader must be used so that content put into the pipe in one
// moment can be shared between multiple reads, as when we send all of the
// responses to the binary in one moment in testing
var reader = bufio.NewReader(os.Stdin)

func fetchOrDefaultWithPrompt(msg, def string) string {
	response := errorRetry
	for i := 0; i < 5; i++ {
		if i == 4 {
			response = errorExit
		}
		formattedDefault := ""
		if def != "" {
			formattedDefault = fmt.Sprintf(" [default: %s]", def)
		}
		fmt.Printf("%s%s ", msg, formattedDefault)
		line, err := reader.ReadString('\n')
		if err != nil {
			response(fmt.Sprintf("could not read the value: %v", err))
			continue
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			return def
		}
		return line
	}
	// dead code below
	return ""
}

func updateProwConfig(config initConfig, releaseRepo string) error {
	configPath := path.Join(releaseRepo, ciopconfig.ConfigInRepoPath)
	agent := prowconfig.Agent{}
	if err := agent.Start(configPath, "", nil, ""); err != nil {
		return fmt.Errorf("could not load Prow configuration: %w", err)
	}

	prowConfig := agent.Config()
	editProwConfig(prowConfig, config)

	data, err := yaml.Marshal(prowConfig)
	if err != nil {
		return fmt.Errorf("could not marshal Prow configuration: %w", err)
	}

	return ioutil.WriteFile(configPath, data, 0644)
}

func editProwConfig(prowConfig *prowconfig.Config, config initConfig) {
	fmt.Println(`
Updating Prow configuration ...`)
	queries := prowConfig.Tide.Queries.QueryMap()
	existing := queries.ForRepo(prowconfig.OrgRepo{Org: config.Org, Repo: config.Repo})
	var existingStrings []string
	for _, query := range existing {
		existingStrings = append(existingStrings, query.Query())
	}
	if len(existing) > 0 {
		fmt.Printf(`The following "tide" queries were found that already apply to %s/%s:

%v

No additional "tide" queries will be added.
`, config.Org, config.Repo, strings.Join(existingStrings, "\n"))
		return
	}

	// this is a bit hacky but simple -- we have a couple types of tide interactions
	// and we can set defaults by piggy backing off of other repos we know that are
	// doing it right
	var copyCatQueries prowconfig.TideQueries
	switch {
	case config.Promotes && config.PromotesWithOpenShift:
		copyCatQueries = queries.ForRepo(prowconfig.OrgRepo{Org: "openshift", Repo: "cluster-version-operator"})
	case !config.PromotesWithOpenShift:
		copyCatQueries = queries.ForRepo(prowconfig.OrgRepo{Org: "openshift", Repo: "ci-tools"})
	}

	orgRepo := fmt.Sprintf("%s/%s", config.Org, config.Repo)
	for i := range prowConfig.Tide.Queries {
		for _, copyCat := range copyCatQueries {
			if reflect.DeepEqual(prowConfig.Tide.Queries[i], copyCat) {
				prowConfig.Tide.Queries[i].Repos = append(prowConfig.Tide.Queries[i].Repos, orgRepo)
			}
		}
	}
}

func updatePluginConfig(config initConfig, releaseRepo string) error {
	fmt.Println(`
Updating Prow plugin configuration ...`)
	configPath := path.Join(releaseRepo, ciopconfig.PluginConfigInRepoPath)
	agent := plugins.ConfigAgent{}
	if err := agent.Load(configPath, []string{filepath.Dir(ciopconfig.PluginConfigFile)}, "_pluginconfig.yaml", false); err != nil {
		return fmt.Errorf("could not load Prow plugin configuration: %w", err)
	}

	pluginConfig := agent.Config()
	editPluginConfig(pluginConfig, config)

	data, err := yaml.Marshal(pluginConfig)
	if err != nil {
		return fmt.Errorf("could not marshal Prow plugin configuration: %w", err)
	}

	return ioutil.WriteFile(configPath, data, 0644)
}

func editPluginConfig(pluginConfig *plugins.Configuration, config initConfig) {
	orgRepo := fmt.Sprintf("%s/%s", config.Org, config.Repo)
	_, orgRegistered := pluginConfig.Plugins[config.Org]
	_, repoRegistered := pluginConfig.Plugins[orgRepo]
	switch {
	case !orgRegistered && !repoRegistered:
		// the repo needs all plugins
		fmt.Println(`
No prior Prow plugin configuration was found for this organization or repository.
Ensure that webhooks are set up for Prow to watch GitHub state.`)
		pluginConfig.Plugins[orgRepo] = plugins.OrgPlugins{Plugins: append(pluginConfig.Plugins["openshift"].Plugins, pluginConfig.Plugins["openshift/origin"].Plugins...)}
	case orgRegistered && !repoRegistered:
		// we just need the repo-specific bits
		pluginConfig.Plugins[orgRepo] = plugins.OrgPlugins{Plugins: pluginConfig.Plugins["openshift/origin"].Plugins}
	}

	_, orgRegisteredExternal := pluginConfig.ExternalPlugins[config.Org]
	_, repoRegisteredExternal := pluginConfig.ExternalPlugins[orgRepo]
	if !orgRegisteredExternal && !repoRegisteredExternal {
		// the repo needs all plugins
		pluginConfig.ExternalPlugins[orgRepo] = pluginConfig.ExternalPlugins["openshift"]
	}

	// TODO: make PR to remove trigger config
	// TODO: update bazel and make PR for exposing LGTM and Approval configs
	no := false
	pluginConfig.Approve = append(pluginConfig.Approve, plugins.Approve{
		Repos:               []string{orgRepo},
		RequireSelfApproval: &no,
		LgtmActsAsApprove:   false,
	})
	pluginConfig.Lgtm = append(pluginConfig.Lgtm, plugins.Lgtm{
		Repos:            []string{orgRepo},
		ReviewActsAsLgtm: true,
	})
}

func createCIOperatorConfig(config initConfig, releaseRepo string) error {
	fmt.Println(`
Generating CI Operator configuration ...`)
	info := api.Metadata{
		Org:    "openshift",
		Repo:   "origin",
		Branch: "master",
	}
	originPath := path.Join(releaseRepo, ciopconfig.CiopConfigInRepoPath, info.RelativePath())
	var originConfig *api.ReleaseBuildConfiguration
	if err := ciopconfig.OperateOnCIOperatorConfig(originPath, func(configuration *api.ReleaseBuildConfiguration, _ *ciopconfig.Info) error {
		originConfig = configuration
		return nil
	}); err != nil {
		return fmt.Errorf("failed to load configuration for openshift/origin: %w", err)
	}

	generated := generateCIOperatorConfig(config, originConfig.PromotionConfiguration)
	return generated.CommitTo(path.Join(releaseRepo, ciopconfig.CiopConfigInRepoPath))
}

func generateCIOperatorConfig(config initConfig, originConfig *api.PromotionConfiguration) ciopconfig.DataWithInfo {
	generated := ciopconfig.DataWithInfo{
		Info: ciopconfig.Info{
			Metadata: api.Metadata{
				Org:    config.Org,
				Repo:   config.Repo,
				Branch: config.Branch,
			},
		},
		Configuration: api.ReleaseBuildConfiguration{
			BinaryBuildCommands:     config.BuildCommands,
			TestBinaryBuildCommands: config.TestBuildCommands,
			Tests:                   []api.TestStepConfiguration{},
			Resources: map[string]api.ResourceRequirements{"*": {
				Limits:   map[string]string{"memory": "4Gi"},
				Requests: map[string]string{"memory": "200Mi", "cpu": "100m"},
			}},
		},
	}

	if config.CanonicalGoRepository != "" {
		generated.Configuration.CanonicalGoRepository = &config.CanonicalGoRepository
	}

	if config.Promotes {
		generated.Configuration.PromotionConfiguration = &api.PromotionConfiguration{
			Namespace: originConfig.Namespace,
			Name:      originConfig.Name,
		}
		generated.Configuration.ReleaseTagConfiguration = &api.ReleaseTagConfiguration{
			Namespace: originConfig.Namespace,
			Name:      originConfig.Name,
		}
		if config.PromotesWithOpenShift {
			workflow := "openshift-e2e-aws"
			generated.Configuration.Tests = append(generated.Configuration.Tests, api.TestStepConfiguration{
				As: "e2e-aws",
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					Workflow:       &workflow,
					ClusterProfile: "aws",
				},
			})
		}
	}

	if config.NeedsBase || config.NeedsOS {
		if generated.Configuration.BaseImages == nil {
			generated.Configuration.BaseImages = map[string]api.ImageStreamTagReference{}
		}
	}

	if config.NeedsBase {
		generated.Configuration.BaseImages["base"] = api.ImageStreamTagReference{
			Namespace: originConfig.Namespace,
			Name:      originConfig.Name,
			Tag:       "base",
		}
	}

	if config.NeedsOS {
		generated.Configuration.BaseImages["os"] = api.ImageStreamTagReference{
			Namespace: "openshift",
			Name:      "centos",
			Tag:       "7",
		}
	}

	generated.Configuration.BuildRootImage = &api.BuildRootImageConfiguration{
		ImageStreamTagReference: &api.ImageStreamTagReference{
			Namespace: "openshift",
			Name:      "release",
			Tag:       fmt.Sprintf("golang-%s", config.GoVersion),
		},
	}

	for _, test := range config.Tests {
		generated.Configuration.Tests = append(generated.Configuration.Tests, api.TestStepConfiguration{
			As:       test.As,
			Commands: test.Command,
			ContainerTestConfiguration: &api.ContainerTestConfiguration{
				From: test.From,
			},
		})
	}

	for _, test := range config.CustomE2E {
		t := api.TestStepConfiguration{
			As: test.As,
			MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
				Workflow:       determineWorkflowFromClusterPorfile(test.Profile),
				ClusterProfile: test.Profile,
				Test: []api.TestStep{
					{
						LiteralTestStep: &api.LiteralTestStep{
							As:        test.As,
							Commands:  test.Command,
							From:      "src",
							Resources: api.ResourceRequirements{Requests: map[string]string{"cpu": "100m"}},
						},
					},
				},
			},
		}

		if test.Cli {
			t.MultiStageTestConfiguration.Test[0].Cli = "latest"
		}

		generated.Configuration.Tests = append(generated.Configuration.Tests, t)
	}

	if config.ReleaseType != "" {
		release := api.UnresolvedRelease{}
		switch config.ReleaseType {
		case "nightly":
			release.Candidate = &api.Candidate{
				Product:      api.ReleaseProductOCP,
				Architecture: api.ReleaseArchitectureAMD64,
				Stream:       api.ReleaseStreamNightly,
				Version:      config.ReleaseVersion,
			}
		case "published":
			release.Release = &api.Release{
				Architecture: api.ReleaseArchitectureAMD64,
				Channel:      api.ReleaseChannelStable,
				Version:      config.ReleaseVersion,
			}
		}
		generated.Configuration.Releases = map[string]api.UnresolvedRelease{api.LatestReleaseName: release}
	}
	return generated
}

func determineWorkflowFromClusterPorfile(clusterProfile api.ClusterProfile) *string {
	var ret string
	switch clusterProfile {
	case api.ClusterProfileAWS:
		ret = "ipi-aws"
	case api.ClusterProfileAzure:
		ret = "ipi-azure"
	case api.ClusterProfileGCP:
		ret = "ipi-gcp"
	}
	return &ret
}
