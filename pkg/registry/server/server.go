package server

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/metrics"

	"github.com/openshift/ci-tools/pkg/api"
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

func injectTestFromQuery(w http.ResponseWriter, r *http.Request) (*api.MetadataWithTest, error) {
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

func ResolveConfigWithInjectedTest(configs Getter, resolver Resolver, resolverMetrics *metrics.Metrics) http.HandlerFunc {
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

		inject, err := injectTestFromQuery(w, r)
		if err != nil {
			// injectTestFromQuery deals with setting status code and writing response
			// so we need to just log the error here
			metrics.RecordError("invalid query", resolverMetrics.ErrorRate)
			logrus.WithError(err).Warning("failed to read query from request")
			return
		}
		injectFromConfig, err := configs.GetMatchingConfig(inject.Metadata)
		if err != nil {
			metrics.RecordError("config not found", resolverMetrics.ErrorRate)
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "failed to get config to inject from: %v", err)
			logger.WithError(err).Warning("failed to get config")
			return
		}
		configWithInjectedTest, err := config.WithPresubmitFrom(&injectFromConfig, inject.Test)
		if err != nil {
			metrics.RecordError("test injection failed", resolverMetrics.ErrorRate)
			w.WriteHeader(http.StatusInternalServerError) // TODO: Can be be 400 in some cases but meh
			fmt.Fprintf(w, "failed to inject test into config: %v", err)
			logger.WithError(err).Warning("failed to inject test into config")
			return
		}

		resolveAndRespond(resolver, *configWithInjectedTest, w, logger, resolverMetrics)
	}
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

		encoded, err := ioutil.ReadAll(r.Body)
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
