// The purpose of this tool is to read a peribolos configuration
// file, get the admins/members of a given organization and
// update the users of a specific group in an Openshift cluster.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/config/org"

	v1 "github.com/openshift/api/user/v1"
	userV1 "github.com/openshift/client-go/user/clientset/versioned/typed/user/v1"

	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	group           string
	peribolosConfig string
	org             string

	dryRun bool
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.group, "group", "", "The group that will be updated in the cluster.")
	fs.StringVar(&o.peribolosConfig, "peribolos-config", "", "Peribolos configuration file")
	fs.StringVar(&o.org, "org", "", "Org from peribolos configuration")

	fs.BoolVar(&o.dryRun, "dry-run", false, "Print the generated group without updating it")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func validateOptions(o options) error {
	if len(o.group) == 0 {
		return fmt.Errorf("--group is not specified")
	}
	if len(o.peribolosConfig) == 0 {
		return fmt.Errorf("--peribolos-config is not specified")
	}
	if len(o.org) == 0 {
		return fmt.Errorf("--org is not specified")
	}
	return nil
}

func getUserV1Client() (*userV1.UserV1Client, error) {
	clusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("could not load cluster clusterConfig: %v", err)
	}

	userV1Client, err := userV1.NewForConfig(clusterConfig)
	if err != nil {
		return nil, fmt.Errorf("could not create user openshift client: %v", err)
	}

	return userV1Client, nil
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather options")
	}
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	logger := logrus.WithField("group", o.group)
	dryLogger := steps.NewDryLogger(false)

	b, err := ioutil.ReadFile(o.peribolosConfig)
	if err != nil {
		logger.WithError(err).Fatal("could not read peribolos configuration file")
	}

	var peribolosConfig org.FullConfig
	if err := yaml.Unmarshal(b, &peribolosConfig); err != nil {
		logger.WithError(err).Fatal("failed to unmarshal peribolos config")
	}

	var userV1Client *userV1.UserV1Client
	if !o.dryRun {
		client, err := getUserV1Client()
		if err != nil {
			logger.WithError(err).Fatal("could not get user client")
		}
		userV1Client = client
	}

	users := sets.NewString()
	users.Insert(peribolosConfig.Orgs[o.org].Admins...)
	users.Insert(peribolosConfig.Orgs[o.org].Members...)

	var action func(*v1.Group) (*v1.Group, error)
	group := &v1.Group{
		ObjectMeta: metav1.ObjectMeta{
			Name: o.group,
		},
	}

	if o.dryRun {
		action = func(g *v1.Group) (*v1.Group, error) {
			dryLogger.AddObject(group.DeepCopyObject())
			if err := dryLogger.Log(); err != nil {
				return g, fmt.Errorf("error while parsing dry logger's objects: %v", err)
			}
			return g, nil
		}
	} else {
		if existing, err := userV1Client.Groups().Get(o.group, metav1.GetOptions{}); err == nil {
			group = existing
			action = userV1Client.Groups().Update
		} else if err != nil && (kerrors.IsNotFound(err) || kerrors.IsForbidden(err) && o.dryRun) {
			group = &v1.Group{ObjectMeta: metav1.ObjectMeta{Name: o.group}}
			action = userV1Client.Groups().Create
		} else {
			logger.WithError(err).Fatal("couldn't get group from cluster")
		}
	}

	group.Users = users.List()
	if _, err := action(group); err != nil {
		logger.WithError(err).Fatal("couldn't sync group to the cluster")
	}
}
