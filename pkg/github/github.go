package github

import (
	"fmt"
	"io/ioutil"
	"net/http"
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
	return func(path string) ([]byte, error) {
		url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", org, repo, branch, path)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to construct request: %w", err)
		}
		if o.BasicAuthUser != "" {
			req.SetBasicAuth(o.BasicAuthUser, o.BasicAuthPassword)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to GET %s: %w", url, err)
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("got unexpected http status code %d, response body: %s", resp.StatusCode, string(body))
		}
		return body, nil
	}
}
