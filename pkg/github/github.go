package github

import (
	"fmt"
	"io"
	"net/http"

	"github.com/hashicorp/go-retryablehttp"
)

type Opts struct {
	// The username to use for basic auth
	BasicAuthUser string
	// The token to use for basic auth
	BasicAuthPassword string
}

type Opt func(*Opts)

func WithAuthentication(username, token string) Opt {
	return func(o *Opts) {
		o.BasicAuthUser = username
		o.BasicAuthPassword = token
	}
}

// FileGetter is a function that downloads the file from the provided path via raw.githubusercontent.com to avoid getting rate limited.
// It returns a nil error on 404.
// TODO: Rethink the 404 behavior?
type FileGetter func(path string) ([]byte, error)

// FileGetterFactory returns a GithubFileGetter that downloads files from raw.githubusercontent.com for the provided org/repo/branch
// It avoids getting ratelimited by using raw.githubusercontent.com. Because it is using a plain http client it can be heavily paralellized
// without killing the machine. It supports private repositories when configured WithAuthentication.
func FileGetterFactory(org, repo, branch string, opts ...Opt) FileGetter {
	o := Opts{}
	for _, opt := range opts {
		opt(&o)
	}
	client := retryablehttp.NewClient()
	client.Logger = nil
	return func(path string) ([]byte, error) {
		url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", org, repo, branch, path)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to construct request: %w", err)
		}
		if o.BasicAuthUser != "" {
			req.SetBasicAuth(o.BasicAuthUser, o.BasicAuthPassword)
		}
		resp, err := client.StandardClient().Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to GET %s: %w", url, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body when getting %s: %w", url, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("got unexpected http status code %d when getting %s, response body: %s", resp.StatusCode, url, string(body))
		}
		return body, nil
	}
}
