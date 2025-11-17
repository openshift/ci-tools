package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/metrics"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load/agents"
)

const (
	OrgQuery     = "org"
	RepoQuery    = "repo"
	BranchQuery  = "branch"
	VariantQuery = "variant"

	InjectFromOrgQuery     = "injectTestFromOrg"
	InjectFromRepoQuery    = "injectTestFromRepo"
	InjectFromBranchQuery  = "injectTestFromBranch"
	InjectFromVariantQuery = "injectTestFromVariant"
	InjectTestQuery        = "injectTest"
)

// NameQuery is used for fetching cluster profile details by its name
const (
	NameQuery = "name"
)

type Resolver interface {
	ResolveConfig(config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error)
}

type Getter interface {
	// GetMatchingConfig loads a configuration that matches the metadata,
	// allowing for regex matching on branch names.
	GetMatchingConfig(metadata api.Metadata) (api.ReleaseBuildConfiguration, error)
}

func MetadataFromQuery(w http.ResponseWriter, r *http.Request) (api.Metadata, error) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusNotImplemented)
		err := fmt.Errorf("expected GET, got %s", r.Method)
		if _, errWrite := w.Write([]byte(http.StatusText(http.StatusNotImplemented))); errWrite != nil {
			return api.Metadata{}, fmt.Errorf("%s and writing the response body failed with %w", err.Error(), errWrite)
		}
		return api.Metadata{}, err
	}

	var metadata api.Metadata
	for query, field := range map[string]*string{
		OrgQuery:    &metadata.Org,
		RepoQuery:   &metadata.Repo,
		BranchQuery: &metadata.Branch,
	} {
		value := r.URL.Query().Get(query)
		if value == "" {
			MissingQuery(w, query)
			return metadata, fmt.Errorf("missing query %s", query)
		}
		*field = value
	}
	metadata.Variant = r.URL.Query().Get(VariantQuery)

	return metadata, nil
}

func MissingQuery(w http.ResponseWriter, field string) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, "%s query missing or incorrect", field)
}

func resolveAndRespond(resolver Resolver, config api.ReleaseBuildConfiguration, w http.ResponseWriter, logger *logrus.Entry, resolverMetrics *metrics.Metrics) {
	config, err := resolver.ResolveConfig(config)
	if err != nil {
		metrics.RecordError("failed to resolve config with registry", resolverMetrics.ErrorRate)
		w.WriteHeader(http.StatusBadRequest)
		if _, writeErr := w.Write([]byte(fmt.Sprintf("failed to resolve config: %v", err))); writeErr != nil {
			logger.WithError(writeErr).Warning("failed to write body after config resolving failed")
		}
		fmt.Fprintf(w, "failed to resolve config with registry: %v", err)
		logger.WithError(err).Warning("failed to resolve config with registry")
		return
	}
	jsonConfig, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		metrics.RecordError("failed to marshal config", resolverMetrics.ErrorRate)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "failed to marshal config to JSON: %v", err)
		logger.WithError(err).Errorf("failed to marshal config to JSON")
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(jsonConfig); err != nil {
		logrus.WithError(err).Error("Failed to write response")
	}
}

func getInjectTestFromQuery(w http.ResponseWriter, r *http.Request) (*api.MetadataWithTest, error) {
	var ret api.MetadataWithTest

	if r.Method != "GET" {
		w.WriteHeader(http.StatusNotImplemented)
		err := fmt.Errorf("expected GET, got %s", r.Method)
		if _, errWrite := w.Write([]byte(http.StatusText(http.StatusNotImplemented))); errWrite != nil {
			err = fmt.Errorf("%s and writing the response body failed with %w", err.Error(), errWrite)
		}
		return &ret, err
	}

	for query, field := range map[string]*string{
		InjectFromOrgQuery:    &ret.Org,
		InjectFromRepoQuery:   &ret.Repo,
		InjectFromBranchQuery: &ret.Branch,
		InjectTestQuery:       &ret.Test,
	} {
		value := r.URL.Query().Get(query)
		if value == "" {
			MissingQuery(w, query)
			return &ret, fmt.Errorf("missing query %s", query)
		}
		*field = value
	}
	ret.Variant = r.URL.Query().Get(InjectFromVariantQuery)

	return &ret, nil
}

func injectTest(injectTo api.ReleaseBuildConfiguration, configs Getter, resolverMetrics *metrics.Metrics, w http.ResponseWriter, r *http.Request, logger *logrus.Entry) *api.ReleaseBuildConfiguration {
	inject, err := getInjectTestFromQuery(w, r)
	if err != nil {
		// getInjectTestFromQuery deals with setting status code and writing response
		// so we need to just log the error here
		metrics.RecordError("invalid query", resolverMetrics.ErrorRate)
		logrus.WithError(err).Warning("failed to read query from request")
		return nil
	}
	injectFromConfig, err := configs.GetMatchingConfig(inject.Metadata)
	if err != nil {
		metrics.RecordError("config not found", resolverMetrics.ErrorRate)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "failed to get config to inject from: %v", err)
		logger.WithError(err).Warning("failed to get config")
		return nil
	}
	configWithInjectedTest, err := injectTo.WithPresubmitFrom(&injectFromConfig, inject.Test)
	if err != nil {
		metrics.RecordError("test injection failed", resolverMetrics.ErrorRate)
		w.WriteHeader(http.StatusInternalServerError) // TODO: Can be be 400 in some cases but meh
		fmt.Fprintf(w, "failed to inject test into config: %v", err)
		logger.WithError(err).Warning("failed to inject test into config")
		return nil
	}

	return configWithInjectedTest
}

func ResolveConfig(configs Getter, resolver Resolver, resolverMetrics *metrics.Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return
		}
		metadata, err := MetadataFromQuery(w, r)
		if err != nil {
			// MetadataFromQuery deals with setting status code and writing response
			// so we need to just log the error here
			metrics.RecordError("invalid query", resolverMetrics.ErrorRate)
			logrus.WithError(err).Warning("failed to read query from request")
			return
		}
		logger := logrus.WithFields(api.LogFieldsFor(metadata))

		config, err := configs.GetMatchingConfig(metadata)
		if err != nil {
			metrics.RecordError("config not found", resolverMetrics.ErrorRate)
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "failed to get config: %v", err)
			logger.WithError(err).Warning("failed to get config")
			return
		}
		resolveAndRespond(resolver, config, w, logger, resolverMetrics)
	}
}

func ResolveLiteralConfig(resolver Resolver, resolverMetrics *metrics.Metrics) http.HandlerFunc {
	logger := logrus.NewEntry(logrus.New())
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return
		}

		encoded, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Could not read unresolved config from request body."))
			return
		}
		unresolvedConfig := api.ReleaseBuildConfiguration{}
		if err = json.Unmarshal(encoded, &unresolvedConfig); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Could not parse request body as unresolved config."))
			return
		}
		resolveAndRespond(resolver, unresolvedConfig, w, logger, resolverMetrics)
	}
}

func ResolveAndMergeConfigsAndInjectTest(configs Getter, resolver Resolver, resolverMetrics *metrics.Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return
		}
		metadataList, err := MetadataEntriesFromQuery(w, r)
		if err != nil {
			// MetadataFromQuery deals with setting status code and writing response
			// so we need to just log the error here
			metrics.RecordError("invalid query", resolverMetrics.ErrorRate)
			logrus.WithError(err).Warning("failed to read query from request")
			return
		}
		logger := logrus.WithField("merged", "true")

		// If the requested injected test is from one of the configs to be merged, we will use that entire config as the base to merge into
		injected, err := getInjectTestFromQuery(w, r)
		if err != nil {
			metrics.RecordError("invalid query", resolverMetrics.ErrorRate)
			logrus.WithError(err).Warning("failed to read inject query from request")
		}
		var metadataToMerge []api.Metadata
		var mergedConfig *api.ReleaseBuildConfiguration
		var injectedTestIncluded bool
		for _, metadata := range metadataList {
			if injected.Metadata == metadata {
				config, err := configs.GetMatchingConfig(metadata)
				if err != nil {
					metrics.RecordError("config not found", resolverMetrics.ErrorRate)
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprintf(w, "failed to get config: %v", err)
					logger.WithError(err).Warning("failed to get config")
					return
				}
				mergedConfig = &config
				injectedTestIncluded = true
			} else {
				metadataToMerge = append(metadataToMerge, metadata)
			}
		}
		if mergedConfig == nil {
			mergedConfig = &api.ReleaseBuildConfiguration{}
		}
		defaultMissingConfigValues(mergedConfig)
		for _, metadata := range metadataToMerge {
			configLogger := logger.WithFields(api.LogFieldsFor(metadata))
			configLogger.Info("requested metadata to be merged")
			config, err := configs.GetMatchingConfig(metadata)
			if err != nil {
				metrics.RecordError("config not found", resolverMetrics.ErrorRate)
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprintf(w, "failed to get config: %v", err)
				configLogger.WithError(err).Warning("failed to get config")
				return
			}
			ref := fmt.Sprintf("%s.%s", metadata.Org, metadata.Repo)

			mergedConfig.BuildRootImages[ref] = *config.BuildRootImage

			for key, image := range config.BaseImages {
				imageRef := fmt.Sprintf("%s-%s", key, ref)
				mergedConfig.BaseImages[imageRef] = image
			}
			if config.BinaryBuildCommands != "" {
				mergedConfig.BinaryBuildCommandsList = append(mergedConfig.BinaryBuildCommandsList, api.RefCommands{
					Ref:      ref,
					Commands: config.BinaryBuildCommands,
				})
			}
			if config.TestBinaryBuildCommands != "" {
				mergedConfig.TestBinaryBuildCommandsList = append(mergedConfig.TestBinaryBuildCommandsList, api.RefCommands{
					Ref:      ref,
					Commands: config.TestBinaryBuildCommands,
				})
			}
			if config.RpmBuildCommands != "" {
				mergedConfig.RpmBuildCommandsList = append(mergedConfig.RpmBuildCommandsList, api.RefCommands{
					Ref:      ref,
					Commands: config.RpmBuildCommands,
				})
			}
			if config.RpmBuildLocation != "" {
				mergedConfig.RpmBuildLocationList = append(mergedConfig.RpmBuildLocationList, api.RefLocation{
					Ref:      ref,
					Location: config.RpmBuildLocation,
				})
			}
			for key, image := range config.BaseRPMImages {
				imageRef := fmt.Sprintf("%s-%s", key, ref)
				mergedConfig.BaseRPMImages[imageRef] = image
			}
			if config.Operator != nil {
				if mergedConfig.Operator == nil {
					mergedConfig.Operator = config.Operator
				} else {
					//TODO: when merging multiple configs with 'operator' defined we could have conflicts, we could handle these better, but it is unlikely to come up
					mergedConfig.Operator.Bundles = append(mergedConfig.Operator.Bundles, config.Operator.Bundles...)
					mergedConfig.Operator.Substitutions = append(mergedConfig.Operator.Substitutions, config.Operator.Substitutions...)
				}
			}
			if config.CanonicalGoRepository != nil {
				mergedConfig.CanonicalGoRepositoryList = append(mergedConfig.CanonicalGoRepositoryList, api.RefRepository{
					Ref:        ref,
					Repository: *config.CanonicalGoRepository,
				})
			}
			// Clear the single-value field after migrating to list format to avoid conflicts
			// and ensure only CanonicalGoRepositoryList is used in the merged configuration
			mergedConfig.CanonicalGoRepository = nil
			for step, resources := range config.Resources {
				if step == "*" { // * is special, and the ref should not be appended, it will be merged to use the greatest value instead
					if existing, ok := mergedConfig.Resources["*"]; ok {
						replaceIfGreater := func(resourceType string) {
							existingValue, err := resource.ParseQuantity(existing.Requests[resourceType])
							if err != nil {
								logger.WithError(err).Warnf("couldn't parse existing '%s' resource quantity", resourceType)
								return
							}
							value, err := resource.ParseQuantity(resources.Requests[resourceType])
							if err != nil {
								logger.WithError(err).Warnf("couldn't parse '%s' resource quantity", resourceType)
								return
							}
							if existingValue.Cmp(value) < 0 { // This value is higher than existing
								mergedConfig.Resources["*"].Requests[resourceType] = resources.Requests[resourceType]
							}
						}
						replaceIfGreater("memory")
						replaceIfGreater("cpu")
					} else {
						mergedConfig.Resources["*"] = api.ResourceRequirements{
							Requests: resources.Requests,
							// We cannot set Limits for * because other configs may not be able to fall under them
						}
					}
				} else {
					stepWithRef := fmt.Sprintf("%s-%s", step, ref)
					mergedConfig.Resources[stepWithRef] = resources
				}
			}
			if len(config.Releases) > 0 && len(mergedConfig.Releases) == 0 {
				// Since the release configs "should" be identical, we can just use the first one we come across
				mergedConfig.Releases = config.Releases
			}

			for i := range config.Images {
				image := config.Images[i]
				if image.From != "" {
					image.From = api.PipelineImageStreamTagReference(fmt.Sprintf("%s-%s", image.From, ref))
				}
				inputs := make(map[string]api.ImageBuildInputs)
				for name, input := range image.Inputs {
					inputs[fmt.Sprintf("%s-%s", name, ref)] = input
				}
				image.Inputs = inputs
				image.To = api.PipelineImageStreamTagReference(fmt.Sprintf("%s-%s", image.To, ref))
				image.Ref = ref
				mergedConfig.Images = append(mergedConfig.Images, image)
			}

			// Attempt to handle a few simple raw_step types on a best-effort basis
			for i := range config.RawSteps {
				rawStep := config.RawSteps[i]
				modifiedStep := rawStep.DeepCopy()
				if rawStep.RPMImageInjectionStepConfiguration != nil {
					to := fmt.Sprintf("%s-%s", rawStep.RPMImageInjectionStepConfiguration.To, ref)
					modifiedStep.RPMImageInjectionStepConfiguration.To = api.PipelineImageStreamTagReference(to)
					from := fmt.Sprintf("%s-%s", rawStep.RPMImageInjectionStepConfiguration.From, ref)
					modifiedStep.RPMImageInjectionStepConfiguration.From = api.PipelineImageStreamTagReference(from)
				} else if rawStep.ProjectDirectoryImageBuildStepConfiguration != nil {
					to := fmt.Sprintf("%s-%s", rawStep.ProjectDirectoryImageBuildStepConfiguration.To, ref)
					modifiedStep.ProjectDirectoryImageBuildStepConfiguration.To = api.PipelineImageStreamTagReference(to)
					from := fmt.Sprintf("%s-%s", rawStep.ProjectDirectoryImageBuildStepConfiguration.From, ref)
					modifiedStep.ProjectDirectoryImageBuildStepConfiguration.From = api.PipelineImageStreamTagReference(from)
					modifiedStep.ProjectDirectoryImageBuildStepConfiguration.Ref = ref
				} else if rawStep.PipelineImageCacheStepConfiguration != nil {
					to := fmt.Sprintf("%s-%s", rawStep.PipelineImageCacheStepConfiguration.To, ref)
					modifiedStep.PipelineImageCacheStepConfiguration.To = api.PipelineImageStreamTagReference(to)
					from := fmt.Sprintf("%s-%s", rawStep.PipelineImageCacheStepConfiguration.From, ref)
					modifiedStep.PipelineImageCacheStepConfiguration.From = api.PipelineImageStreamTagReference(from)
				} else if rawStep.OutputImageTagStepConfiguration != nil {
					from := fmt.Sprintf("%s-%s", rawStep.OutputImageTagStepConfiguration.From, ref)
					modifiedStep.OutputImageTagStepConfiguration.From = api.PipelineImageStreamTagReference(from)
					//We don't want to change the 'to' here as it will likely land in stable and shouldn't be modified
				} else {
					configLogger.Warnf("raw_steps[%d] in config is of an unsupported type for multi-pr payload testing, this is not handled and may result in errors", i)
				}
				mergedConfig.RawSteps = append(mergedConfig.RawSteps, *modifiedStep)
			}
		}

		// Only inject the test if the config containing it is not already included in the result
		if !injectedTestIncluded {
			mergedConfig = injectTest(*mergedConfig, configs, resolverMetrics, w, r, logger)
		}
		if mergedConfig != nil {
			resolveAndRespond(resolver, *mergedConfig, w, logger, resolverMetrics)
		}
	}
}

func defaultMissingConfigValues(mergedConfig *api.ReleaseBuildConfiguration) {
	if mergedConfig.InputConfiguration.BuildRootImages == nil {
		mergedConfig.InputConfiguration.BuildRootImages = make(map[string]api.BuildRootImageConfiguration)
	}
	if mergedConfig.InputConfiguration.BaseImages == nil {
		mergedConfig.InputConfiguration.BaseImages = make(map[string]api.ImageStreamTagReference)
	}
	if mergedConfig.InputConfiguration.BaseRPMImages == nil {
		mergedConfig.InputConfiguration.BaseRPMImages = make(map[string]api.ImageStreamTagReference)
	}
	if mergedConfig.Resources == nil {
		mergedConfig.Resources = make(api.ResourceConfiguration)
	}
}

func MetadataEntriesFromQuery(w http.ResponseWriter, r *http.Request) ([]api.Metadata, error) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusNotImplemented)
		err := fmt.Errorf("expected GET, got %s", r.Method)
		if _, errWrite := w.Write([]byte(http.StatusText(http.StatusNotImplemented))); errWrite != nil {
			return []api.Metadata{}, fmt.Errorf("%s and writing the response body failed with %w", err.Error(), errWrite)
		}
		return []api.Metadata{}, err
	}

	orgs := strings.Split(r.URL.Query().Get(OrgQuery), ",")
	repos := strings.Split(r.URL.Query().Get(RepoQuery), ",")
	branches := strings.Split(r.URL.Query().Get(BranchQuery), ",")
	variants := strings.Split(r.URL.Query().Get(VariantQuery), ",")
	variantsExist := false
	for _, variant := range variants {
		if variant != "" {
			variantsExist = true
			break
		}
	}

	if len(orgs) != len(repos) || len(orgs) != len(branches) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Passed: orgs (%d), repos (%d), and branches (%d) do not match", len(orgs), len(repos), len(branches))
	}
	if variantsExist && len(orgs) != len(variants) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "If any variants are passed, there must be one for each ref. Blank variants are allowed.")
		return nil, fmt.Errorf("malformed request: variant must be set for each ref (a blank variant is allowed for placeholder) or none of them")
	}

	metadata := make(sets.Set[api.Metadata])
	for i, org := range orgs {
		element := api.Metadata{
			Org:    org,
			Repo:   repos[i],
			Branch: branches[i],
		}
		if variantsExist {
			element.Variant = variants[i]
		}
		metadata.Insert(element)
	}

	result := metadata.UnsortedList()
	sort.SliceStable(result, func(i, j int) bool {
		one := result[i]
		two := result[j]
		if one.Org != two.Org {
			return one.Org < two.Org
		}
		if one.Repo != two.Repo {
			return one.Repo < two.Repo
		}
		if one.Branch != two.Branch {
			return one.Branch < two.Branch
		}
		return one.Variant < two.Variant
	})
	return result, nil
}

// ResolveClusterProfile extracts the cluster profile name from request query
// and in the response provides all details about the cluster profile
func ResolveClusterProfile(agent agents.RegistryAgent, resolverMetrics *metrics.Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return
		}
		profileNameFromQuery, err := ProfileNameFromQuery(w, r)
		if err != nil {
			metrics.RecordError("invalid cluster profiles query", resolverMetrics.ErrorRate)
			logrus.WithError(err).Warning("failed to read query from request")
			return
		}
		profileDetails, err := agent.GetClusterProfileDetails(profileNameFromQuery)
		if err != nil {
			metrics.RecordError("cluster profile not found", resolverMetrics.ErrorRate)
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "could not find cluster profile %s: %v", profileNameFromQuery, err)
			logrus.WithError(err).Warning(fmt.Sprintf("cluster profile %s not found", profileNameFromQuery))
			return
		}
		jsonContent, err := json.MarshalIndent(profileDetails, "", "  ")
		if err != nil {
			metrics.RecordError("failed to marshal cluster profile to JSON", resolverMetrics.ErrorRate)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to marshall cluster profile %s to JSON: %v", profileNameFromQuery, err)
			logrus.WithError(err).Errorf("failed to marshal cluster profile %s to JSON: %v", profileNameFromQuery, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(jsonContent); err != nil {
			logrus.WithError(err).Errorf("Failed to write response: %v", err)
		}
	}
}

// ProfileNameFromQuery gets the cluster profile name from the request query
func ProfileNameFromQuery(w http.ResponseWriter, r *http.Request) (string, error) {
	profileName := r.URL.Query().Get(NameQuery)
	if profileName == "" {
		MissingQuery(w, NameQuery)
		return "", fmt.Errorf("missing query %s", NameQuery)
	}
	return profileName, nil
}
