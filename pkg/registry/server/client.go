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
	"github.com/openshift/ci-tools/pkg/api/configresolver"
)

type ResolverClient interface {
	Config(*api.Metadata) (*api.ReleaseBuildConfiguration, error)
	ConfigWithTest(base *api.Metadata, testSource *api.MetadataWithTest) (*api.ReleaseBuildConfiguration, error)
	Resolve([]byte) (*api.ReleaseBuildConfiguration, error)
	ClusterProfile(profileName string) (*api.ClusterProfileDetails, error)
	IntegratedStream(namespace, name string) (*configresolver.IntegratedStream, error)
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

func (r *resolverClient) ConfigWithTest(base *api.Metadata, testSource *api.MetadataWithTest) (*api.ReleaseBuildConfiguration, error) {
	logrus.Infof("Loading configuration from %s for %s", r.Address, base.AsString())
	endpoint := fmt.Sprintf("%s/mergeConfigsWithInjectedTest", r.Address)
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
	data, err := doRequest(req)
	if err != nil {
		return nil, err
	}
	configSpecHTTP := &api.ReleaseBuildConfiguration{}
	err = json.Unmarshal(data, configSpecHTTP)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config from configresolver: invalid configuration: %w\nvalue:\n%s", err, string(data))
	}
	return configSpecHTTP, nil
}

// doRequest makes a request to config resolver and returns the response body
func doRequest(req *http.Request) ([]byte, error) {
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
	return io.ReadAll(resp.Body)
}

// ClusterProfile gets the info about a desired cluster profile by creating a request
// to config resolver
func (r *resolverClient) ClusterProfile(profileName string) (*api.ClusterProfileDetails, error) {
	logrus.Infof("Loading information from %s for cluster profile %s", r.Address, profileName)
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/clusterProfile", r.Address), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for configresolver: %w", err)
	}
	query := req.URL.Query()
	query.Add(NameQuery, profileName)
	req.URL.RawQuery = query.Encode()

	data, err := doRequest(req)
	if err != nil {
		return nil, err
	}

	cp := &api.ClusterProfileDetails{}
	if err = json.Unmarshal(data, cp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s cluster profile information from configresolver: %w\nvalue:\n%s", profileName, err, string(data))
	}
	return cp, nil
}

// IntegratedStream gets the info about an integrated stream by creating a request
// to config resolver
func (r *resolverClient) IntegratedStream(namespace, name string) (*configresolver.IntegratedStream, error) {
	logrus.Infof("Loading information from %s for integrated stream %s/%s", r.Address, namespace, name)
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/integratedStream", r.Address), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for configresolver: %w", err)
	}
	query := req.URL.Query()
	query.Add("namespace", namespace)
	query.Add("name", name)
	req.URL.RawQuery = query.Encode()

	data, err := doRequest(req)
	if err != nil {
		return nil, err
	}

	stream := &configresolver.IntegratedStream{}
	if err = json.Unmarshal(data, stream); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s/%s integrated stream from configresolver: %w\nvalue:\n%s", namespace, name, err, string(data))
	}
	return stream, nil
}
