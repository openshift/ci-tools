package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
)

type ResolverClient interface {
	Config(*api.Metadata) (*api.ReleaseBuildConfiguration, error)
	ConfigWithTest(base *api.Metadata, testSource *api.MetadataWithTest, multipleSources bool) (*api.ReleaseBuildConfiguration, error)
	Resolve([]byte) (*api.ReleaseBuildConfiguration, error)
}

func NewResolverClient(address string) ResolverClient {
	return &resolverClient{Address: address}
}

type resolverClient struct {
	Address string
}

func (r *resolverClient) Config(info *api.Metadata) (*api.ReleaseBuildConfiguration, error) {
	logrus.Infof("Loading configuration from %s for %s", r.Address, info.AsString())
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/config", r.Address), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for configresolver: %w", err)
	}
	query := req.URL.Query()
	query.Add(OrgQuery, info.Org)
	query.Add(RepoQuery, info.Repo)
	query.Add(BranchQuery, info.Branch)
	if len(info.Variant) > 0 {
		query.Add(VariantQuery, info.Variant)
	}
	req.URL.RawQuery = query.Encode()
	return configFromResolverRequest(req)
}

func (r *resolverClient) ConfigWithTest(base *api.Metadata, testSource *api.MetadataWithTest, multipleSources bool) (*api.ReleaseBuildConfiguration, error) {
	logrus.Infof("Loading configuration from %s for %s", r.Address, base.AsString())
	endpoint := fmt.Sprintf("%s/configWithInjectedTest", r.Address)
	if multipleSources {
		endpoint = fmt.Sprintf("%s/mergeConfigsWithInjectedTest", r.Address)
	}
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for configresolver: %w", err)
	}
	query := req.URL.Query()
	optional := sets.New[string](VariantQuery, InjectFromVariantQuery)
	for k, v := range map[string]string{
		InjectTestQuery:        testSource.Test,
		InjectFromOrgQuery:     testSource.Org,
		InjectFromRepoQuery:    testSource.Repo,
		InjectFromBranchQuery:  testSource.Branch,
		InjectFromVariantQuery: testSource.Variant,
		OrgQuery:               base.Org,
		RepoQuery:              base.Repo,
		BranchQuery:            base.Branch,
		VariantQuery:           base.Variant,
	} {
		if len(v) == 0 && !optional.Has(k) {
			return nil, fmt.Errorf("param cannot be empty: %s", k)
		}
		query.Add(k, v)
	}

	req.URL.RawQuery = query.Encode()
	return configFromResolverRequest(req)
}

func (r *resolverClient) Resolve(raw []byte) (*api.ReleaseBuildConfiguration, error) {
	// check that the user has sent us something reasonable
	unresolvedConfig := &api.ReleaseBuildConfiguration{}
	if err := yaml.UnmarshalStrict(raw, unresolvedConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal unresolved config: invalid configuration: %w, raw: %v", err, string(raw))
	}
	encoded, err := json.Marshal(unresolvedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal unresolved config: invalid configuration: %w", err)
	}
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/resolve", r.Address), bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("failed to create request for configresolver: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return configFromResolverRequest(req)
}

type adapter struct{}

func (a adapter) format(s string, i ...interface{}) string {
	builder := strings.Builder{}
	builder.WriteString(s)
	for _, x := range i {
		builder.WriteString(" ")
		builder.WriteString(fmt.Sprintf("%v", x))
	}
	return builder.String()
}

func (a adapter) Error(s string, i ...interface{}) {
	logrus.Error(a.format(s, i...))
}

func (a adapter) Info(s string, i ...interface{}) {
	logrus.Info(a.format(s, i...))
}

func (a adapter) Debug(s string, i ...interface{}) {
	logrus.Debug(a.format(s, i...))
}

func (a adapter) Warn(s string, i ...interface{}) {
	logrus.Warn(a.format(s, i...))
}

var _ retryablehttp.LeveledLogger = adapter{}

func configFromResolverRequest(req *http.Request) (*api.ReleaseBuildConfiguration, error) {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 5
	retryClient.Logger = adapter{}
	client := retryClient.StandardClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to configresolver: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var responseBody string
		if data, err := io.ReadAll(resp.Body); err != nil {
			logrus.WithError(err).Warn("Failed to read response body from configresolver.")
		} else {
			responseBody = string(data)
		}
		return nil, fmt.Errorf("got unexpected http %d status code from configresolver: %s", resp.StatusCode, responseBody)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read configresolver response body: %w", err)
	}
	configSpecHTTP := &api.ReleaseBuildConfiguration{}
	err = json.Unmarshal(data, configSpecHTTP)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config from configresolver: invalid configuration: %w\nvalue:\n%s", err, string(data))
	}
	return configSpecHTTP, nil
}
