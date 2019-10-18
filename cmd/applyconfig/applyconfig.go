package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	templateapi "github.com/openshift/api/template/v1"
	templatescheme "github.com/openshift/client-go/template/clientset/versioned/scheme"
)

type level string
type command string

type options struct {
	confirm   bool
	level     level
	user      string
	directory string
}

const (
	standardLevel level = "standard"
	adminLevel    level = "admin"
	allLevel      level = "all"

	ocApply   command = "apply"
	ocProcess command = "process"
)

const defaultAdminUser = "system:admin"

func (l level) isValid() bool {
	return l == standardLevel || l == adminLevel || l == allLevel
}

func (l level) shouldApplyAdmin() bool {
	return l == adminLevel || l == allLevel
}

func (l level) shouldApplyStandard() bool {
	return l == standardLevel || l == allLevel
}

var adminConfig = regexp.MustCompile(`^admin_.+\.yaml$`)

func gatherOptions() *options {
	opt := &options{}
	var lvl string
	flag.BoolVar(&opt.confirm, "confirm", false, "Set to true to make applyconfig commit the config to the cluster")
	flag.StringVar(&lvl, "level", "standard", "Select which config to apply (standard, admin, all)")
	flag.StringVar(&opt.user, "as", "", "Username to impersonate while applying the config")
	flag.StringVar(&opt.directory, "config-dir", "", "Directory with config to apply")
	flag.Parse()

	opt.level = level(lvl)

	if !opt.level.isValid() {
		fmt.Fprintf(os.Stderr, "--level: must be one of [standard, admin, all]\n")
		os.Exit(1)
	}

	if opt.directory == "" {
		fmt.Fprintf(os.Stderr, "--config-dir must be provided\n")
		os.Exit(1)
	}

	return opt
}

func isAdminConfig(filename string) bool {
	return adminConfig.MatchString(filename)
}

func isStandardConfig(filename string) bool {
	return filepath.Ext(filename) == ".yaml" &&
		!isAdminConfig(filename)
}

func makeOcCommand(cmd command, path, user string, additionalArgs ...string) *exec.Cmd {
	args := []string{string(cmd), "-f", path}
	args = append(args, additionalArgs...)

	if user != "" {
		args = append(args, "--as", user)
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
			fmt.Printf("ERROR: %s: failed to %s\n%s\n", pretty, action, exitError.Stderr)
		} else {
			fmt.Printf("ERROR: %s: failed to execute: %v\n", pretty, err)
		}
		return nil, fmt.Errorf("failed to %s config", action)
	}

	fmt.Printf("%s: OK\n", pretty)
	return output, nil
}

type configApplier struct {
	executor

	path string
	user string
	dry  bool
}

func makeOcApply(path, user string, dry bool) *exec.Cmd {
	cmd := makeOcCommand(ocApply, path, user)
	if dry {
		cmd.Args = append(cmd.Args, "--dry-run")
	}
	return cmd
}

func (c *configApplier) asGenericManifest() error {
	cmd := makeOcApply(c.path, c.user, c.dry)
	_, err := c.runAndCheck(cmd, "apply")
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
		}
	}
	ocProcessCmd := makeOcCommand(ocProcess, c.path, c.user, args...)

	var processed []byte
	var err error
	if processed, err = c.runAndCheck(ocProcessCmd, "process"); err != nil {
		return err
	}

	ocApplyCmd := makeOcApply("-", c.user, c.dry)
	ocApplyCmd.Stdin = bytes.NewBuffer(processed)
	_, err = c.runAndCheck(ocApplyCmd, "apply")
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

func apply(path, user string, dry bool) error {
	do := configApplier{path: path, user: user, dry: dry, executor: &commandExecutor{}}

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

type processFn func(name, path string) error

func applyConfig(rootDir, cfgType string, process processFn) error {
	failures := false
	if err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			if strings.HasPrefix(info.Name(), "_") {
				fmt.Printf("Skipping directory: %s\n", path)
				return filepath.SkipDir
			}
			fmt.Printf("Applying %s config in directory: %s\n", cfgType, path)
			return nil
		}

		if err := process(info.Name(), path); err != nil {
			failures = true
		}

		return nil
	}); err != nil {
		// should not happen
		fmt.Fprintf(os.Stderr, "failed to walk directory '%s': %v\n", rootDir, err)
		return err
	}

	if failures {
		return fmt.Errorf("failed to apply admin config")
	}

	return nil
}

func main() {
	o := gatherOptions()
	var adminErr, standardErr error

	if o.level.shouldApplyAdmin() {
		if o.user == "" {
			o.user = defaultAdminUser
		}

		f := func(name, path string) error {
			if !isAdminConfig(name) {
				return nil
			}
			return apply(path, o.user, !o.confirm)
		}

		adminErr = applyConfig(o.directory, "admin", f)
		if adminErr != nil {
			fmt.Printf("There were failures while applying admin config\n")
		}
	}

	if o.level.shouldApplyStandard() {
		f := func(name, path string) error {
			if !isStandardConfig(name) {
				return nil
			}
			if strings.HasPrefix(name, "_") {
				return nil
			}

			return apply(path, o.user, !o.confirm)
		}

		standardErr = applyConfig(o.directory, "standard", f)
		if standardErr != nil {
			fmt.Printf("There were failures while applying standard config\n")
		}
	}

	if standardErr != nil || adminErr != nil {
		os.Exit(1)
	}

	fmt.Printf("Success!\n")
}
