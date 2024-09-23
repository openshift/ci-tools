package dispatcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/sirupsen/logrus"
)

type Client interface {
	ClusterForJob(jobName string) (string, error)
}

func NewClient(address string) Client {
	return client{Address: address}
}

type client struct {
	Address string
}

func (c client) ClusterForJob(jobName string) (string, error) {
	schedulingRequest := SchedulingRequest{Job: jobName}
	body, err := json.Marshal(schedulingRequest)
	if err != nil {
		return "", fmt.Errorf("could not marshal scheduling request: %w", err)
	}
	req, err := http.NewRequest("POST", c.Address, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("could not create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := doRequest(req)
	if err != nil {
		return "", fmt.Errorf("error performing request: %w", err)
	}
	schedulingResponse := SchedulingResponse{}
	if err = json.Unmarshal(response, &schedulingResponse); err != nil {
		return "", fmt.Errorf("could not parse scheduling response: %w", err)
	}
	return schedulingResponse.Cluster, nil
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

func doRequest(req *http.Request) ([]byte, error) {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 5
	retryClient.Logger = adapter{}
	client := retryClient.StandardClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to dispatcher: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var responseBody string
		if data, err := io.ReadAll(resp.Body); err != nil {
			logrus.WithError(err).Warn("Failed to read response body from dispatcher.")
		} else {
			responseBody = string(data)
		}
		return nil, fmt.Errorf("got unexpected http %d status code from dispatcher: %s", resp.StatusCode, responseBody)
	}
	return io.ReadAll(resp.Body)
}
