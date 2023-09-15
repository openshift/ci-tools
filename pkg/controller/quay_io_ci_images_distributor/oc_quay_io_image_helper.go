package quay_io_ci_images_distributor

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/json"
)

type OCImageMirrorOptions struct {
	RegistryConfig  string
	ContinueOnError bool
	MaxPerRegistry  int
}

type OCImageInfoOptions struct {
	RegistryConfig string
	FilterByOS     string
}

type client struct {
	logger   *logrus.Entry
	executor executor
}

type executor interface {
	Run(args ...string) ([]byte, error)
}

type ocExecutor struct {
	// logger will be used to log git operations
	logger *logrus.Entry
	// oc is the path to the oc binary.
	oc string
	// execute executes a command
	execute func(dir, command string, args ...string) ([]byte, error)
}

func (e *ocExecutor) Run(args ...string) ([]byte, error) {
	logger := e.logger.WithField("args", strings.Join(args, " "))
	b, err := e.execute("", e.oc, args...)
	if err != nil {
		logger.WithError(err).WithField("output", string(b)).Debug("Running command failed.")
	} else {
		logger.Debug("Running command succeeded.")
	}
	return b, err
}

func newOCExecutor(logger *logrus.Entry) (executor, error) {
	oc, err := exec.LookPath("oc")
	if err != nil {
		return nil, err
	}
	return &ocExecutor{
		logger: logger.WithField("client", oc),
		oc:     oc,
		execute: func(dir, command string, args ...string) ([]byte, error) {
			c := exec.Command(command, args...)
			c.Dir = dir
			return c.CombinedOutput()
		},
	}, nil
}

type OCClient interface {
	ImageInfo(image string, options OCImageInfoOptions) (ImageInfo, error)
	ImageMirror(src, dst string, options OCImageMirrorOptions) error
}

type clientFactory struct {
	logger               *logrus.Entry
	ocImageInfoOptions   OCImageInfoOptions
	ocImageMirrorOptions OCImageMirrorOptions
}

type ClientFactoryOpts struct {
	ocImageInfoOptions   OCImageInfoOptions
	ocImageMirrorOptions OCImageMirrorOptions
}

type ClientFactoryOpt func(*ClientFactoryOpts)

func newClientFactory(opts ...ClientFactoryOpt) clientFactory {
	o := ClientFactoryOpts{}
	for _, opt := range opts {
		opt(&o)
	}
	return clientFactory{
		logger:               logrus.WithField("client", "oc"),
		ocImageInfoOptions:   o.ocImageInfoOptions,
		ocImageMirrorOptions: o.ocImageMirrorOptions,
	}
}

func (c *clientFactory) NewClient() (OCClient, error) {
	return c.bootstrapClients()
}

// bootstrapClients returns an oc client and cloner.
func (c *clientFactory) bootstrapClients() (OCClient, error) {
	executor, err := newOCExecutor(c.logger)
	if err != nil {
		return nil, err
	}
	ret := client{
		logger:   c.logger,
		executor: executor,
	}
	return &ret, nil
}

// ImageMirror mirror images from one image repository to another.
func (c *client) ImageMirror(src, dst string, options OCImageMirrorOptions) error {
	return nil
}

// ImageInfo get the info of an image.
func (c *client) ImageInfo(image string, options OCImageInfoOptions) (ImageInfo, error) {
	var ret ImageInfo
	args := []string{"image", "info", image}
	if options.FilterByOS != "" {
		args = append(args, fmt.Sprintf("--filter-by-os=%s", options.FilterByOS))
	}
	if options.RegistryConfig != "" {
		args = append(args, fmt.Sprintf("--registry-config=%s", options.RegistryConfig))
	}
	args = append(args, "--output=json")
	data, err := c.executor.Run(args...)
	if err != nil {
		if isNotFound(data) {
			return ret, nil
		}
		return ret, err
	}
	if err = json.Unmarshal(data, &ret); err != nil {
		return ret, err
	}
	return ret, nil
}

func isNotFound(output []byte) bool {
	return strings.Contains(string(output), "not found:")
}
