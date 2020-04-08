package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	templateapi "github.com/openshift/api/template/v1"
	templatescheme "github.com/openshift/client-go/template/clientset/versioned/scheme"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"
)

type level string
type command string

type options struct {
	confirm     bool
	level       level
	user        *nullableStringFlag
	directories flagutil.Strings
	context     string
	kubeConfig  string
}

const (
	ocApply   command = "apply"
	ocProcess command = "process"
)

const defaultAdminUser = "system:admin"

func (l level) isValid() bool {
	return l == "all"
}

type nullableStringFlag struct {
	val     string
	beenSet bool
}

func (n *nullableStringFlag) String() string {
	return n.val
}

func (n *nullableStringFlag) Set(val string) error {
	n.val = val
	n.beenSet = true
	return nil
}

func gatherOptions() *options {
	opt := &options{user: &nullableStringFlag{}}
	var lvl string
	flag.BoolVar(&opt.confirm, "confirm", false, "Set to true to make applyconfig commit the config to the cluster")
	flag.StringVar(&lvl, "level", "standard", "Select which config to apply (standard, admin, all)")
	flag.Var(opt.user, "as", "Username to impersonate while applying the config")
	flag.Var(&opt.directories, "config-dir", "Directory with config to apply. Can be repeated multiple times.")
	flag.StringVar(&opt.context, "context", "", "Context name to use while applying the config")
	flag.StringVar(&opt.kubeConfig, "kubeconfig", "", "Path to the kubeconfig file to apply the config")
	flag.Parse()

	opt.level = level(lvl)

	if !opt.level.isValid() {
		fmt.Fprintf(os.Stderr, "--level: must be one of [all]\n")
		os.Exit(1)
	}

	if len(opt.directories.Strings()) < 1 || opt.directories.Strings()[0] == "" {
		fmt.Fprintf(os.Stderr, "--config-dir must be provided\n")
		os.Exit(1)
	}

	return opt
}
func makeOcCommand(cmd command, kubeConfig, context, path, user string, additionalArgs ...string) *exec.Cmd {
	args := []string{string(cmd), "-f", path}
	args = append(args, additionalArgs...)

	if user != "" {
		args = append(args, "--as", user)
	}

	if kubeConfig != "" {
		args = append(args, "--kubeconfig", kubeConfig)
	}

	if context != "" {
		args = append(args, "--context", context)
	}

	return exec.Command("oc", args...)
}

type executor interface {
	runAndCheck(cmd *exec.Cmd, action string) ([]byte, error)
}

type commandExecutor struct{}

func (c commandExecutor) runAndCheck(cmd *exec.Cmd, action string) ([]byte, error) {
	var output []byte
	var err error
	pretty := strings.Join(cmd.Args, " ")

	if output, err = cmd.Output(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			logrus.Errorf("%s: failed to %s\n%s", pretty, action, exitError.Stderr)
		} else {
			logrus.WithError(err).Errorf("%s: failed to execute", pretty)
		}
		return nil, fmt.Errorf("failed to %s config", action)
	}

	logrus.Infof("%s: OK", pretty)
	return output, nil
}

type configApplier struct {
	executor

	kubeConfig string
	context    string
	path       string
	user       string
	dry        bool
}

func makeOcApply(kubeConfig, context, path, user string, dry bool) *exec.Cmd {
	cmd := makeOcCommand(ocApply, kubeConfig, context, path, user)
	if dry {
		cmd.Args = append(cmd.Args, "--dry-run")
	}
	return cmd
}

func (c *configApplier) asGenericManifest() error {
	cmd := makeOcApply(c.kubeConfig, c.context, c.path, c.user, c.dry)
	out, err := c.runAndCheck(cmd, "apply")
	logrus.WithField("output", string(out)).Info("Ran apply command")
	return err
}

func (c configApplier) asTemplate(params []templateapi.Parameter) error {
	var args []string
	for _, param := range params {
		if len(param.Generate) > 0 {
			continue
		}
		envValue := os.Getenv(param.Name)
		if len(envValue) > 0 {
			args = append(args, []string{"-p", fmt.Sprintf("%s=%s", param.Name, envValue)}...)
			secrets.addSecrets(envValue)
		}
	}
	ocProcessCmd := makeOcCommand(ocProcess, c.kubeConfig, c.context, c.path, c.user, args...)

	var processed []byte
	var err error
	if processed, err = c.runAndCheck(ocProcessCmd, "process"); err != nil {
		return err
	}

	ocApplyCmd := makeOcApply(c.kubeConfig, c.context, "-", c.user, c.dry)
	ocApplyCmd.Stdin = bytes.NewBuffer(processed)
	out, err := c.runAndCheck(ocApplyCmd, "apply")
	logrus.WithField("output", string(out)).Info("Ran apply command")
	return err
}

// isTemplate return true when the content of the stream is an OpenShift template,
// and returns false in all other cases (including when an error occurs while
// reading from input).
// When it is template, return also its parameters.
func isTemplate(input io.Reader) ([]templateapi.Parameter, bool) {
	var contents bytes.Buffer
	if _, err := io.Copy(&contents, input); err != nil {
		return nil, false
	}

	obj, _, err := templatescheme.Codecs.UniversalDeserializer().Decode(contents.Bytes(), nil, nil)
	if err != nil {
		return nil, false
	}
	t, ok := obj.(*templateapi.Template)
	if ok {
		return t.Parameters, true
	}

	return nil, false
}

func apply(kubeConfig, context, path, user string, dry bool) error {
	do := configApplier{kubeConfig: kubeConfig, context: context, path: path, user: user, dry: dry, executor: &commandExecutor{}}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	params, isTemplate := isTemplate(file)
	if isTemplate {
		return do.asTemplate(params)
	}
	return do.asGenericManifest()
}

func applyConfig(rootDir string, o *options) error {
	failures := false
	if err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if skip, err := fileFilter(info, path); skip || err != nil {
			return err
		}

		if err := apply(o.kubeConfig, o.context, path, o.user.val, !o.confirm); err != nil {
			failures = true
		}

		return nil
	}); err != nil {
		// should not happen
		logrus.WithError(err).Errorf("failed to walk directory '%s'", rootDir)
		return err
	}

	if failures {
		return fmt.Errorf("failed to apply config")
	}

	return nil
}

func fileFilter(info os.FileInfo, path string) (bool, error) {
	if info.IsDir() {
		if strings.HasPrefix(info.Name(), "_") {
			logrus.Infof("Skipping directory: %s", path)
			return false, filepath.SkipDir
		}
		logrus.Infof("Applying config in directory: %s", path)
		return true, nil
	}

	if filepath.Ext(info.Name()) != ".yaml" {
		return true, nil
	}

	if strings.HasPrefix(info.Name(), "_") {
		return true, nil
	}

	return false, nil
}

type secretGetter struct {
	sync.RWMutex
	secrets sets.String
}

func (g *secretGetter) addSecrets(newSecrets ...string) {
	g.Lock()
	defer g.Unlock()
	g.secrets.Insert(newSecrets...)
}

func (g *secretGetter) getSecrets() sets.String {
	g.RLock()
	defer g.RUnlock()
	return g.secrets
}

var (
	secrets *secretGetter
)

func init() {
	secrets = &secretGetter{secrets: sets.NewString()}
	logrus.SetFormatter(logrusutil.NewCensoringFormatter(logrus.StandardLogger().Formatter, secrets.getSecrets))
}

func main() {
	o := gatherOptions()
	var hadErr bool

	if !o.user.beenSet {
		o.user.val = defaultAdminUser
	}

	for _, dir := range o.directories.Strings() {
		if err := applyConfig(dir, o); err != nil {
			hadErr = true
			logrus.WithError(err).Error("There were failures while applying config")
		}
	}

	if hadErr {
		os.Exit(1)
	}

	logrus.Infof("Success!")
}
