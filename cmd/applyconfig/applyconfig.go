package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hashicorp/go-version"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"

	templateapi "github.com/openshift/api/template/v1"
	templatescheme "github.com/openshift/client-go/template/clientset/versioned/scheme"
)

type command string
type dryRunMethod string

type options struct {
	user        *nullableStringFlag
	directories flagutil.Strings
	context     string
	kubeConfig  string
	dryRun      dryRunMethod
}

const (
	ocApply   command = "apply"
	ocProcess command = "process"
	ocVersion command = "version"

	dryNone   dryRunMethod = ""
	dryAuto   dryRunMethod = "auto"
	dryServer dryRunMethod = "server"
	dryClient dryRunMethod = "client"
)

const defaultAdminUser = "system:admin"

var validDryRunMethods = []string{string(dryAuto), string(dryClient), string(dryServer)}

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
	// nonempy dryRun is a safe default (empty means not a dry run)
	// TODO(muller): client dry run is default until we pass server one.
	opt := &options{user: &nullableStringFlag{}, dryRun: dryClient}

	var confirm bool
	flag.BoolVar(&confirm, "confirm", false, "Set to true to make applyconfig commit the config to the cluster")
	flag.Var(opt.user, "as", "Username to impersonate while applying the config")
	flag.Var(&opt.directories, "config-dir", "Directory with config to apply. Can be repeated multiple times.")
	flag.StringVar(&opt.context, "context", "", "Context name to use while applying the config")
	flag.StringVar(&opt.kubeConfig, "kubeconfig", "", "Path to the kubeconfig file to apply the config")

	var dryMethod string
	dryRunMethods := strings.Join(validDryRunMethods, ",")
	flag.StringVar(&dryMethod, "dry-run-method", string(opt.dryRun), fmt.Sprintf("Method to use when running when --confirm is not set to true (valid values: %s)", dryRunMethods))
	flag.Parse()

	if len(opt.directories.Strings()) < 1 || opt.directories.Strings()[0] == "" {
		fmt.Fprintf(os.Stderr, "--config-dir must be provided\n")
		os.Exit(1)
	}

	switch dryRunMethod(dryMethod) {
	case dryAuto, dryServer, dryClient:
		if confirm {
			opt.dryRun = dryNone
		} else {
			opt.dryRun = dryRunMethod(dryMethod)
		}
	default:
		fmt.Fprintf(os.Stderr, "--dry-run-method must be one of: %s", dryRunMethods)
		os.Exit(1)
	}

	return opt
}
func makeOcCommand(cmd command, kubeConfig, context, path, user string, additionalArgs ...string) *exec.Cmd {
	args := []string{string(cmd)}
	if path != "" {
		args = append(args, "-f", path)
	}
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
	dry        dryRunMethod
}

func makeOcApply(kubeConfig, context, path, user string, dry dryRunMethod) *exec.Cmd {
	cmd := makeOcCommand(ocApply, kubeConfig, context, path, user)
	switch dry {
	case dryAuto:
		logrus.Warn("BUG: Automated dryrun detection should be performed earlier; Using server-side validation")
		fallthrough
	case dryServer:
		cmd.Args = append(cmd.Args, "--dry-run=server", "--validate=true")
	case dryClient:
		cmd.Args = append(cmd.Args, "--dry-run=client")
	default:
		panic(fmt.Sprintf("BUG: Unknown dry run method '%s' received, this should never happen", string(dry)))
	case dryNone:
		// No additional args needed
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

func apply(kubeConfig, context, path, user string, dry dryRunMethod) error {
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

		// oc-cli works with the symlinks targeting files
		// here we need to handle symlinks targeting folders recursively
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("failed to readlink %s: %w", path, err)
			}
			targetFileInfo, err := os.Stat(target)
			if err != nil {
				return fmt.Errorf("failed to Stat %s: %w", target, err)
			}
			if targetFileInfo.IsDir() {
				logrus.Infof("replace the symlink folder %s with the target %s", path, target)
				if err := applyConfig(target, o); err != nil {
					failures = true
				}
				return nil
			}
		}

		if skip, err := fileFilter(info, path); skip || err != nil {
			return err
		}

		if err := apply(o.kubeConfig, o.context, path, o.user.val, o.dryRun); err != nil {
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

type ocVersionOutput struct {
	Openshift string `json:"openshiftVersion"`
}

func selectDryRun(v ocVersionOutput) dryRunMethod {
	minOpenshift := version.Must(version.NewVersion("4.5.0"))

	// `oc` against `api.ci` does not contain the openshift version field, so if it's missing, we need to assume
	// we're running against something old
	if v.Openshift == "" {
		logrus.Warning("oc version did not provide openshift version: guessing too old, will continue with client dry-run")
		return dryClient
	}

	// Generally we want to prefer the stronger (server) validation unless
	// we *know* we need to use the weak, client one -> so use server on errors
	openshift, err := version.NewVersion(v.Openshift)
	if err != nil {
		logrus.WithError(err).Warning("Cannot parse openshift version; will continue with server dry-run")
		return dryServer
	}

	if openshift.GreaterThanOrEqual(minOpenshift) {
		return dryServer
	}

	return dryClient
}

func detectDryRunMethod(kubeconfig, context, username string) dryRunMethod {
	logrus.Info("Detecting dry run capabilities")

	// We always prefer stronger (server) validation unless we *know* we need
	// to use the weak, client one -> so use server on errors
	method := dryServer

	executor := commandExecutor{}
	cmd := makeOcCommand(ocVersion, kubeconfig, context, "", username, "--output=json")
	out, err := executor.runAndCheck(cmd, "detect server version")
	if err != nil {
		logrus.WithError(err).Warning("Failed to detect dry run method from client and server versions; will continue with server dry-run")
		return method
	}

	var v ocVersionOutput
	if err := json.Unmarshal(out, &v); err != nil {
		return method
	}

	method = selectDryRun(v)
	logrus.WithFields(logrus.Fields{
		"openshift-version": v.Openshift,
		"method":            method,
	}).Info("Selected dry run method")

	return method
}

func main() {
	o := gatherOptions()

	if !o.user.beenSet {
		o.user.val = defaultAdminUser
	}

	if o.dryRun == dryAuto {
		o.dryRun = detectDryRunMethod(o.kubeConfig, o.context, o.user.val)
	}

	var hadErr bool
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
