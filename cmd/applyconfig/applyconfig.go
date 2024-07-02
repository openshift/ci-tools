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
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	templateapi "github.com/openshift/api/template/v1"
	templatescheme "github.com/openshift/client-go/template/clientset/versioned/scheme"

	"github.com/openshift/ci-tools/pkg/api/nsttl"
	"github.com/openshift/ci-tools/pkg/prowconfigutils"
	"github.com/openshift/ci-tools/pkg/secrets"
)

type command string
type dryRunMethod string
type applyMethod string

type options struct {
	user        *nullableStringFlag
	directories flagutil.Strings
	ignoreFiles flagutil.Strings
	context     string
	kubeConfig  string
	dryRun      dryRunMethod
	apply       applyMethod
}

const (
	ocApply   command = "apply"
	ocProcess command = "process"
	ocVersion command = "version"

	dryNone   dryRunMethod = ""
	dryAuto   dryRunMethod = "auto"
	dryServer dryRunMethod = "server"
	dryClient dryRunMethod = "client"

	applyServer applyMethod = "server"
	applyClient applyMethod = "client"
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
	// nonempty dryRun is a safe default (empty means not a dry run)
	opt := &options{user: &nullableStringFlag{}, dryRun: dryAuto, apply: applyClient}

	var confirm bool
	flag.BoolVar(&confirm, "confirm", false, "Set to true to make applyconfig commit the config to the cluster")
	flag.Var(opt.user, "as", "Username to impersonate while applying the config")
	flag.Var(&opt.directories, "config-dir", "Directory with config to apply. Can be repeated multiple times.")
	flag.Var(&opt.ignoreFiles, "ignore-file", "File to ignore. Can be repeated multiple times.")
	flag.StringVar(&opt.context, "context", "", "Context name to use while applying the config")
	flag.StringVar(&opt.kubeConfig, "kubeconfig", "", "Path to the kubeconfig file to apply the config")

	var dryMethod string
	dryRunMethods := strings.Join(validDryRunMethods, ",")
	flag.StringVar(&dryMethod, "dry-run-method", string(opt.dryRun), fmt.Sprintf("Method to use when running when --confirm is not set to true (valid values: %s)", dryRunMethods))

	var applyMethod string
	applyMethods := strings.Join([]string{string(applyServer), string(applyClient)}, ",")
	flag.StringVar(&applyMethod, "apply-method", string(opt.apply), fmt.Sprintf("Method to use when applying the config (valid values: %s). Server-side apply is always enabled for file with names start with '_SS'.", applyMethods))

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
			logrus.Infof("%s: failed to %s\n%s", pretty, action, exitError.Stderr)
			output = exitError.Stderr
		} else {
			logrus.WithError(err).Errorf("%s: failed to execute", pretty)
		}
		return output, fmt.Errorf("failed to %s config", action)
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
	apply      applyMethod
	censor     *secrets.DynamicCensor
}

func makeOcApply(kubeConfig, context, path, user string, dry dryRunMethod, apply applyMethod) *exec.Cmd {
	cmd := makeOcCommand(ocApply, kubeConfig, context, path, user, "-o", "name")
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

	fileName := filepath.Base(path)
	if strings.HasPrefix(fileName, "SS_") {
		logrus.Info("Use server-side apply for ", fileName)
		cmd.Args = append(cmd.Args, "--server-side=true")
	}

	switch apply {
	case applyServer:
		cmd.Args = append(cmd.Args, "--server-side=true")
	case applyClient:
		// No additional args needed
	}
	return cmd
}

var namespaceNotFound = regexp.MustCompile(`Error from server \(NotFound\):.*namespaces "(.*)" not found.*`)

func inferMissingNamespaces(applyOutput []byte) sets.Set[string] {
	var ret sets.Set[string]
	for _, line := range strings.Split(string(applyOutput), "\n") {
		line := strings.TrimSpace(line)
		if matches := namespaceNotFound.FindStringSubmatch(line); len(matches) == 2 {
			if ret == nil {
				ret = sets.New[string]()
			}
			ret.Insert(matches[1])
		}
	}
	return ret
}

type namespaceActions struct {
	Created sets.Set[string]
	Assumed sets.Set[string]
}

func extractNamespaces(applyOutput []byte) sets.Set[string] {
	var namespaces sets.Set[string]
	for _, line := range strings.Split(string(applyOutput), "\n") {
		line := strings.TrimSpace(line)
		if strings.HasPrefix(line, "namespace/") {
			if namespaces == nil {
				namespaces = sets.New[string]()
			}
			namespaces.Insert(strings.TrimPrefix(line, "namespace/"))
		}
	}
	return namespaces
}

func (c *configApplier) doWithRetry(do func() ([]byte, error)) (namespaceActions, error) {
	var namespaces namespaceActions

	// This function analyses the output of oc apply and searches for messages that
	// indicate that the apply failed because a namespace was missing. When running
	// in server-side dry-runs, these failures may be bogus because such namespaces
	// would be created in an earlier or current manifest. The function compensates
	// this by creating these missing namespaces and retrying the failed oc apply.
	// These namespaces are created annotated for ci-ns-ttl-controller to reap it
	// so unmerged PRs do not clutter the cluster. The annotations are cleaned up
	// by the `oc apply` run by applyconfig in non-dry mode after merge.
	compensate := func(out []byte) bool {
		if c.dry != dryServer {
			return false
		}
		logrus.Info("Running in server-side dry run mode, attempting to recover previous failure by providing missing namespaces")
		missingNamespaces := inferMissingNamespaces(out)
		if len(missingNamespaces) == 0 {
			logrus.Error("Previous failure was not caused by missing namespaces, cannot recover")
			return false
		}
		for ns := range missingNamespaces {
			logrus.WithField("missing-namespace", ns).Infof("Temporarily creating missing namespace")
			namespace := v1.Namespace{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Namespace",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:        ns,
					Annotations: map[string]string{nsttl.AnnotationCleanupDurationTTL: (time.Hour).String()},
				},
			}
			rawNs, err := json.Marshal(namespace)
			if err != nil {
				logrus.WithField("missing-namespace", ns).WithError(err).Errorf("failed to prepare provisional namespace")
				return false
			}
			// We *must* create the namespace via marshaling and `oc apply` and NOT e.g.
			// via oc create / oc annotate. Otherwise, the TTL annotations would not be
			// cleaned up when the manifests are actually applied post-merge to the
			// cluster and NS TTL controller would reap the production namespace.
			ocApplyCmd := makeOcApply(c.kubeConfig, c.context, "-", c.user, dryNone, c.apply)
			ocApplyCmd.Stdin = bytes.NewBuffer(rawNs)
			if out, err := c.runAndCheck(ocApplyCmd, "apply"); err != nil {
				logrus.WithField("missing-namespace", ns).WithField("output", out).WithError(err).Errorf("Failed to create provisional namespace")
				return false
			}
			if namespaces.Assumed == nil {
				namespaces.Assumed = sets.New[string]()
			}
			namespaces.Assumed.Insert(ns)
		}
		return true
	}

	out, err := do()
	for err != nil {
		if retry := compensate(out); !retry {
			logrus.WithField("output", string(out)).Errorf("Apply command failed (not recoverable)")
			return namespaces, err
		}
		out, err = do()
	}

	if namespaces.Created == nil {
		namespaces.Created = sets.New[string]()
	}

	namespaces.Created = namespaces.Created.Union(extractNamespaces(out))
	return namespaces, nil
}

func (c *configApplier) asGenericManifest() (namespaceActions, error) {
	do := func() ([]byte, error) {
		cmd := makeOcApply(c.kubeConfig, c.context, c.path, c.user, c.dry, c.apply)
		out, err := c.runAndCheck(cmd, "apply")
		return out, err
	}

	return c.doWithRetry(do)
}

func (c configApplier) asTemplate(params []templateapi.Parameter) (namespaceActions, error) {
	var args []string
	for _, param := range params {
		if len(param.Generate) > 0 {
			continue
		}
		envValue := os.Getenv(param.Name)
		if len(envValue) > 0 {
			args = append(args, []string{"-p", fmt.Sprintf("%s=%s", param.Name, envValue)}...)
			c.censor.AddSecrets(envValue)
		}
	}
	ocProcessCmd := makeOcCommand(ocProcess, c.kubeConfig, c.context, c.path, c.user, args...)

	var processed []byte
	var err error
	if processed, err = c.runAndCheck(ocProcessCmd, "process"); err != nil {
		return namespaceActions{}, err
	}

	do := func() ([]byte, error) {
		ocApplyCmd := makeOcApply(c.kubeConfig, c.context, "-", c.user, c.dry, c.apply)
		ocApplyCmd.Stdin = bytes.NewBuffer(processed)
		out, err := c.runAndCheck(ocApplyCmd, "apply")
		return out, err
	}
	return c.doWithRetry(do)
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

func apply(kubeConfig, context, path, user string, dry dryRunMethod, apply applyMethod, censor *secrets.DynamicCensor) (namespaceActions, error) {
	do := configApplier{
		kubeConfig: kubeConfig,
		context:    context,
		path:       path,
		user:       user,
		dry:        dry,
		apply:      apply,
		executor:   &commandExecutor{},
		censor:     censor,
	}

	file, err := os.Open(path)
	if err != nil {
		return namespaceActions{}, err
	}
	defer file.Close()

	params, isTemplate := isTemplate(file)
	if isTemplate {
		return do.asTemplate(params)
	}
	return do.asGenericManifest()
}

func applyConfig(rootDir string, o *options, createdNamespaces sets.Set[string], censor *secrets.DynamicCensor) (sets.Set[string], error) {
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
				if namespaces, err := applyConfig(target, o, createdNamespaces, censor); err != nil {
					failures = true
				} else {
					createdNamespaces = createdNamespaces.Union(namespaces)
				}
				return nil
			}
		}

		if skip, err := fileFilter(info, path, o.ignoreFiles); skip || err != nil {
			return err
		}

		namespaces, err := apply(o.kubeConfig, o.context, path, o.user.val, o.dryRun, o.apply, censor)
		if err != nil {
			failures = true
			return nil
		}

		// Bookkeep which namespaces are created over time even if we run in dry-run
		// mode. In server side dry-mode, applyConfig recovers failures caused by
		// missing namespaces by actually creating them - this implements an
		// *assumption* that these namespaces would be created by earlier or current
		// apply command.
		createdNamespaces = createdNamespaces.Union(namespaces.Created)
		if len(namespaces.Assumed) > 0 {
			var assumedNames []string
			for ns := range namespaces.Assumed {
				assumedNames = append(assumedNames, ns)
			}
			logrus.WithField("path", path).WithField("assumed-namespaces", strings.Join(assumedNames, ", ")).Info("Apply was successful only under namespace creation assumption")
			if missing := namespaces.Assumed.Difference(createdNamespaces); len(missing) > 0 {
				for ns := range missing {
					logrus.WithField("path", path).WithField("namespace", ns).Error("Step passed assuming a namespace was previously created but it was not")
				}
				failures = true
			}
		}

		return nil
	}); err != nil {
		// should not happen
		logrus.WithError(err).Errorf("failed to walk directory '%s'", rootDir)
		return createdNamespaces, err
	}

	if failures {
		return createdNamespaces, fmt.Errorf("failed to apply config")
	}

	return createdNamespaces, nil
}

func fileFilter(info os.FileInfo, path string, ignoreFiles flagutil.Strings) (bool, error) {
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

	if ignoreFiles.StringSet().Has(path) {
		return true, nil
	}

	return false, nil
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
		logrus.WithError(err).Warning("Failed to detect dry run method from client and server versions; will continue with server dry-run")
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
	logrusutil.ComponentInit()
	o := gatherOptions()

	if !o.user.beenSet {
		o.user.val = defaultAdminUser
	}

	if o.dryRun == dryAuto {
		o.dryRun = detectDryRunMethod(o.kubeConfig, o.context, o.user.val)
	}
	censor := secrets.NewDynamicCensor()
	logrus.SetFormatter(logrusutil.NewFormatterWithCensor(logrus.StandardLogger().Formatter, &censor))

	prowDisabledClusters, err := prowconfigutils.ProwDisabledClusters(nil)
	if err != nil {
		logrus.WithError(err).Warn("Failed to get Prow disable clusters")
	} else if len(prowDisabledClusters) > 0 && targetClusterIsDisabled(sets.New[string](prowDisabledClusters...)) {
		logrus.WithField("prowDisabledClusters", prowDisabledClusters).Info("Apply no manifests to Prow disabled clusters")
		return
	}

	var hadErr bool
	createdNamespaces := sets.New[string]()
	for _, dir := range o.directories.Strings() {
		namespaces, err := applyConfig(dir, o, createdNamespaces, &censor)
		if err != nil {
			hadErr = true
			logrus.WithError(err).Error("There were failures while applying config")
		}
		createdNamespaces = createdNamespaces.Union(namespaces)
	}

	if hadErr {
		os.Exit(1)
	}

	logrus.Infof("Success!")
}

func targetClusterIsDisabled(disabledClusters sets.Set[string]) bool {
	jobName := os.Getenv(downwardapi.JobNameEnv)
	if jobName != "" {
		for _, c := range disabledClusters.UnsortedList() {
			if strings.Contains(jobName, c) {
				return true
			}
		}
	}
	return false
}
