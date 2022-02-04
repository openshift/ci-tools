package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	ldapv3 "github.com/go-ldap/ldap/v3"
	"github.com/sirupsen/logrus"

	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"
	"sigs.k8s.io/yaml"

	templatev1 "github.com/openshift/api/template/v1"
	userv1 "github.com/openshift/api/user/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/group"
)

type options struct {
	logLevelRaw      string
	logLevel         logrus.Level
	manifestDirRaw   flagutil.Strings
	manifestDirs     sets.String
	ldapServer       string
	validateSubjects bool
	groupsFile       string
	configFile       string
	mappingFile      string
}

func parseOptions() *options {
	opts := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&opts.logLevelRaw, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	fs.Var(&opts.manifestDirRaw, "manifest-dir", "directory containing Kubernetes manifests. Can be specified multiple times.")
	fs.BoolVar(&opts.validateSubjects, "validate-subjects", false, "Whether to validate subjects such as group and users in the manifests")
	fs.StringVar(&opts.ldapServer, "ldap-server", "ldap.corp.redhat.com", "LDAP server")
	fs.StringVar(&opts.groupsFile, "groups-file", "/tmp/groups.yaml", "The file to store the groups in yaml format")
	fs.StringVar(&opts.configFile, "config-file", "", "The yaml file storing the config file for the groups")
	fs.StringVar(&opts.mappingFile, "mapping-file", "", "File used to store the mapping results of m(github_login)=kerberos_id.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse args")
	}
	return opts
}

func (o *options) validate() error {
	level, err := logrus.ParseLevel(o.logLevelRaw)
	if err != nil {
		return fmt.Errorf("invalid log level specified: %w", err)
	}
	o.logLevel = level

	values := o.manifestDirRaw.Strings()
	if len(values) == 0 {
		return fmt.Errorf("--manifest-dir must be set")
	}
	if o.validateSubjects && o.mappingFile != "" {
		return fmt.Errorf("--mapping-file cannot be set when --validate-subjects is true")
	}
	return nil
}

func addSchemes() error {
	if err := userv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add userv1 to scheme: %w", err)
	}
	if err := rbacv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add rbacv1 to scheme: %w", err)
	}
	if err := templatev1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add templatev1 to scheme: %w", err)
	}
	return nil
}

func main() {
	logrusutil.ComponentInit()

	opts := parseOptions()

	if err := opts.validate(); err != nil {
		logrus.WithError(err).Fatal("failed to validate the option")
	}
	logrus.SetLevel(opts.logLevel)
	opts.manifestDirs = sets.NewString(opts.manifestDirRaw.Strings()...)

	var config *group.Config
	if opts.configFile != "" {
		loadedConfig, err := group.LoadConfig(opts.configFile)
		if err != nil {
			logrus.WithError(err).Fatal("failed to load config")
		}
		config = loadedConfig
	}

	if err := addSchemes(); err != nil {
		logrus.WithError(err).Fatal("failed to add schemes")
	}

	// validate runs as a presubmit which does not have access to Red Hat Intranet
	var conn *ldapv3.Conn
	if !opts.validateSubjects {
		c, err := ldapv3.DialURL(fmt.Sprintf("ldap://%s", opts.ldapServer))
		if err != nil {
			logrus.Fatal(err)
		}
		conn = c
		defer conn.Close()
	}

	groupCollector := newYamlGroupCollector(opts.validateSubjects)
	groupResolver := &ldapGroupResolver{conn: conn}

	if opts.mappingFile != "" {
		mapping, err := groupResolver.getGitHubUserKerberosIDMapping()
		if err != nil {
			logrus.WithError(err).Fatal("failed to get GitHub User and KerberosID mapping")
		}
		bytes, err := yaml.Marshal(mapping)
		if err != nil {
			logrus.WithError(err).Fatal("failed to marshal GitHub User and KerberosID mapping")
		}
		if err := ioutil.WriteFile(opts.mappingFile, bytes, 0644); err != nil {
			logrus.WithField("path", opts.mappingFile).WithError(err).
				Fatal("failed to write GitHub User and KerberosID mapping to file")
		}
		logrus.WithField("path", opts.mappingFile).Info("Saved the mapping")
	}

	groups, err := roverGroups(opts.manifestDirs, config, opts.validateSubjects, groupCollector, groupResolver)
	if err != nil {
		logrus.WithError(err).Fatal("failed to get rover groups")
	}
	data, err := yaml.Marshal(groups)
	if err != nil {
		logrus.WithError(err).Fatal("failed to marshal groups")
	}
	if err := ioutil.WriteFile(opts.groupsFile, data, 0644); err != nil {
		logrus.WithError(err).WithField("file", opts.groupsFile).Fatal("failed to write file")
	}
}

type Group struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type groupResolver interface {
	resolve(name string) (*Group, error)
	getGitHubUserKerberosIDMapping() (map[string]string, error)
}

type groupCollector interface {
	collect(dir string) (sets.String, error)
}

func roverGroups(manifestDirs sets.String, config *group.Config, validateSubjects bool, groupCollector groupCollector, groupResolver groupResolver) (map[string][]string, error) {
	var errs []error

	groupNames := sets.NewString()
	for _, d := range manifestDirs.List() {
		names, err := groupCollector.collect(d)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to collect groups for %s: %w", d, err))
			continue
		}
		groupNames.Insert(names.List()...)
	}

	groupNames.Insert(api.CIAdminsGroupName)

	if config != nil {
		for k, v := range config.Groups {
			if v.RenameTo != "" {
				logrus.WithField("group", v.RenameTo).Info("Skip resolving the renamed group")
				groupNames.Delete(v.RenameTo)
			}
			groupNames.Insert(k)
		}
	}

	groups := map[string][]string{}
	if !validateSubjects {
		for _, name := range groupNames.List() {
			logrus.WithField("group", name).Debug("resolving group ...")
			g, err := groupResolver.resolve(name)
			if err != nil {
				if IsNotFoundError(err) && name != api.CIAdminsGroupName {
					logrus.WithError(err).WithField("group", name).Warn("failed to resolve group")
					continue
				}
				errs = append(errs, fmt.Errorf("failed to resolve group %s: %w", name, err))
				continue
			}
			if l := len(g.Members); name == api.CIAdminsGroupName && l < 3 {
				errs = append(errs, fmt.Errorf("group %s should has at lesat 3 members, found %d", api.CIAdminsGroupName, l))
				continue
			}
			groups[name] = g.Members
		}
	} else {
		logrus.WithField("validateSubjects", validateSubjects).Debug("Skip resolving groups")
	}

	if len(errs) > 0 {
		return nil, kerrors.NewAggregate(errs)
	}

	return groups, nil
}
