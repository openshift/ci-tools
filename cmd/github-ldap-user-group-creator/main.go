package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
	"google.golang.org/api/option"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/prow/config/org"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/logrusutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	userv1 "github.com/openshift/api/user/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/group"
	"github.com/openshift/ci-tools/pkg/prowconfigutils"
	"github.com/openshift/ci-tools/pkg/rover"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

type options struct {
	kubernetesOptions flagutil.KubernetesOptions

	logLevel           string
	dryRun             bool
	deleteInvalidUsers bool
	groupsFile         string
	configFile         string
	maxConcurrency     int

	peribolosConfig        string
	orgFromPeribolosConfig string
	githubUsersFile        string
	gcpCredentialsFile     string
}

func parseOptions() *options {
	opts := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	opts.kubernetesOptions.AddFlags(fs)
	fs.StringVar(&opts.logLevel, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	fs.BoolVar(&opts.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	fs.BoolVar(&opts.deleteInvalidUsers, "delete-invalid-users", false, "If set, delete users that are not in Rover or have no links to GitHub there")
	fs.StringVar(&opts.groupsFile, "groups-file", "", "The yaml file storing the groups")
	fs.StringVar(&opts.configFile, "config-file", "", "The yaml file storing the config file for the groups")
	fs.IntVar(&opts.maxConcurrency, "concurrency", 60, "Maximum number of concurrent in-flight goroutines to handle groups.")
	fs.StringVar(&opts.peribolosConfig, "peribolos-config", "", "Peribolos configuration file")
	fs.StringVar(&opts.orgFromPeribolosConfig, "org-from-peribolos-config", "openshift-priv", "Org from peribolos configuration")
	fs.StringVar(&opts.githubUsersFile, "github-users-file", "", "File used to store GitHub users.")
	fs.StringVar(&opts.gcpCredentialsFile, "gcp-credentials-file", "", "The json file storing the gcp credentials.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse args")
	}
	return opts
}

func (o *options) validate() error {
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid log level specified: %w", err)
	}
	logrus.SetLevel(level)

	if o.githubUsersFile == "" {
		return fmt.Errorf("--github-users-file must not be empty")
	}
	if o.gcpCredentialsFile == "" {
		return fmt.Errorf("--gcp-credentials-file must not be empty")
	}
	if o.groupsFile == "" {
		return fmt.Errorf("--groups-file must not be empty")
	}

	if o.peribolosConfig != "" {
		if o.orgFromPeribolosConfig == "" {
			return fmt.Errorf("--org-from-peribolos-config must be set if --peribolos-config is set")
		}
	}
	return nil
}

const (
	appCIContextName = string(api.ClusterAPPCI)
	toolName         = "github-ldap-user-group-creator"
)

func addSchemes() error {
	if err := userv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add userv1 to scheme: %w", err)
	}
	return nil
}

func main() {
	logrusutil.ComponentInit()

	opts := parseOptions()

	if err := opts.validate(); err != nil {
		logrus.WithError(err).Fatal("failed to validate the option")
	}

	var openshiftPrivAdmins sets.Set[string]
	if opts.peribolosConfig != "" {
		admins, err := getOpenshiftPrivAdmins(opts.peribolosConfig, opts.orgFromPeribolosConfig)
		if err != nil {
			logrus.WithError(err).Fatal("failed to get OpenShiftPrivAdmins")
		}
		openshiftPrivAdmins = admins
		if openshiftPrivAdmins == nil || openshiftPrivAdmins.Len() == 0 {
			logrus.Warn("found no OpenShiftPrivAdmins")
		}
	}

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

	prowDisabledClusters, err := prowconfigutils.ProwDisabledClusters(&opts.kubernetesOptions)
	if err != nil {
		logrus.WithError(err).Warn("Failed to get Prow disable clusters")
	}

	kubeconfigs, err := opts.kubernetesOptions.LoadClusterConfigs()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load kubeconfigs")
	}

	inClusterConfig, hasInClusterConfig := kubeconfigs[kube.InClusterContext]
	delete(kubeconfigs, kube.InClusterContext)
	delete(kubeconfigs, kube.DefaultClusterAlias)

	if _, hasAppCi := kubeconfigs[appCIContextName]; !hasAppCi {
		if !hasInClusterConfig {
			logrus.WithError(err).Fatalf("had no context for '%s' and loading InClusterConfig failed", appCIContextName)
		}
		logrus.Infof("use InClusterConfig for %s", appCIContextName)
		kubeconfigs[appCIContextName] = inClusterConfig
	}

	kubeConfig := kubeconfigs[appCIContextName]
	appCIClient, err := ctrlruntimeclient.New(&kubeConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatalf("could not create client")
	}

	clients := map[string]ctrlruntimeclient.Client{}
	clusters := sets.New[string]()
	for cluster, config := range kubeconfigs {
		clusters.Insert(cluster)
		cluster, config := cluster, config
		if cluster == appCIContextName {
			continue
		}
		client, err := ctrlruntimeclient.New(&config, ctrlruntimeclient.Options{})
		if err != nil {
			logrus.WithError(err).WithField("cluster", cluster).Fatal("could not create client for cluster")
		}
		clients[cluster] = client
	}

	clients[appCIContextName] = appCIClient

	ctx := interrupts.Context()

	users, err := func(path string) ([]rover.User, error) {
		logrus.WithField("path", path).Debug("Loading the GitHub users file ...")
		bytes, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", path, err)
		}
		var users []rover.User
		if err := yaml.Unmarshal(bytes, &users); err != nil {
			return nil, fmt.Errorf("failed to unmarshal: %w", err)
		}
		return users, nil
	}(opts.githubUsersFile)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load the GitHub users")
	}

	gcpClient, err := bigquery.NewClient(ctx, "openshift-gce-devel", option.WithCredentialsFile(opts.gcpCredentialsFile))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create the GCP client")
	}
	defer func() {
		if err := gcpClient.Close(); err != nil {
			logrus.WithError(err).Fatal("Failed to close the GCP client")
		}
	}()

	var userItems []*rover.UserItem
	now := time.Now()
	for _, user := range users {
		userItems = append(userItems, &rover.UserItem{
			Created: now,
			User: rover.User{
				UID:            user.UID,
				GitHubUsername: user.GitHubUsername,
				CostCenter:     user.CostCenter,
			},
		})
	}
	if err := insertRows(ctx, gcpClient, userItems); err != nil {
		logrus.WithError(err).Fatal("Failed to insert users to bigquery")
	}

	mapping := rover.MapGithubToKerberos(users)

	data, err := os.ReadFile(opts.groupsFile)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to read the group file")
	}
	roverGroups := map[string][]string{}
	if err := yaml.Unmarshal(data, &roverGroups); err != nil {
		logrus.WithError(err).Fatal("Failed to unmarshal groups")
	}
	ciAdmins, ok := roverGroups[api.CIAdminsGroupName]
	if !ok {
		logrus.WithField("groupName", api.CIAdminsGroupName).Fatal("Failed to find ci-admins group")
	} else if l := len(ciAdmins); l < 3 {
		logrus.WithField("groupName", api.CIAdminsGroupName).WithField("len", l).Fatal("Require at least 3 members of ci-admins group")
	}

	kerberosIds := sets.New[string]()
	for _, kerberosId := range mapping {
		kerberosIds.Insert(kerberosId)
	}
	if opts.deleteInvalidUsers {
		if err := deleteInvalidUsers(ctx, clients, kerberosIds, sets.New[string](ciAdmins...), opts.dryRun); err != nil {
			logrus.WithError(err).Fatal("Failed to delete users")
		}
	}

	groups, err := makeGroups(openshiftPrivAdmins, opts.peribolosConfig, mapping, roverGroups, config, clusters)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to make groups")
	}

	if err := ensureGroups(ctx, clients, groups, opts.maxConcurrency, opts.dryRun, sets.New[string](prowDisabledClusters...)); err != nil {
		logrus.WithError(err).Fatal("could not ensure groups")
	}
}

func insertRows(ctx context.Context, client *bigquery.Client, users []*rover.UserItem) error {
	inserter := client.Dataset("ci_analysis_us").Table("users").Inserter()
	if err := inserter.Put(ctx, users); err != nil {
		return err
	}
	return nil
}

func getOpenshiftPrivAdmins(peribolosConfig, orgFromPeribolosConfig string) (sets.Set[string], error) {
	b, err := gzip.ReadFileMaybeGZIP(peribolosConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to read peribolos configuration file: %w", err)
	}

	var config org.FullConfig
	if err := yaml.Unmarshal(b, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal peribolos config: %w", err)
	}

	members := sets.New[string]()
	orgConfig, ok := config.Orgs[orgFromPeribolosConfig]
	if !ok {
		return nil, fmt.Errorf("failed to find org %s in peribolos config", orgFromPeribolosConfig)
	}

	members.Insert(orgConfig.Admins...)
	members.Insert(orgConfig.Members...)
	return members, nil
}

type GroupClusters struct {
	Clusters sets.Set[string]
	Group    *userv1.Group
}

var githubRobotIds = sets.New[string]("RH-Cachito", "openshift-bot", "openshift-ci-robot", "openshift-merge-robot", "openshift-cherrypick-robot")

func deleteInvalidUsers(ctx context.Context, clients map[string]ctrlruntimeclient.Client,
	kerberosIDs sets.Set[string], ciAdmins sets.Set[string], dryRun bool) error {

	var errs []error
	for cluster, client := range clients {
		usersToDelete, err := getUsersWithoutKerberosID(ctx, client, cluster, kerberosIDs)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get users on cluster %s: %w", cluster, err))
			continue
		}
		for user, identites := range usersToDelete {
			if user == "backplane-cluster-admin" {
				logrus.WithField("cluster", cluster).Info("Skip deleting user backplane-cluster-admin")
				continue
			}
			if ciAdmins.Has(user) {
				// should never happen
				logrus.WithField("cluster", cluster).WithField("user", user).Info("Attempt to delete admin! Skipping...")
				continue
			}
			logrus.WithField("cluster", cluster).WithField("user", user).Info("Deleting user...")
			if !dryRun {
				var err error
				if err = client.Delete(ctx, &userv1.User{ObjectMeta: metav1.ObjectMeta{Name: user}}); err != nil && !errors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("failed to delete user %s on cluster %s: %w", user, cluster, err))

				}
				if err == nil {
					logrus.WithField("cluster", cluster).WithField("user", user).Info("Deleted successfully.")
				}

			}
			for _, identity := range identites {
				logrus.WithField("cluster", cluster).WithField("user", user).WithField("identity", identity).Info("Deleting identity...")
				if dryRun {
					continue
				}
				var err error
				if err = client.Delete(ctx, &userv1.Identity{ObjectMeta: metav1.ObjectMeta{Name: identity}}); err != nil && !errors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("failed to delete identity %s on cluster %s: %w", identity, cluster, err))
				}
				if err == nil {
					logrus.WithField("cluster", cluster).WithField("user", user).WithField("identity", identity).Info("Deleted successfully.")
				}
			}
		}
		logrus.WithField("cluster", cluster).Info("Deleting invalid users and identites is finished!")
	}
	return kerrors.NewAggregate(errs)
}

// Returns users without kerberosID and their identities
func getUsersWithoutKerberosID(ctx context.Context, client ctrlruntimeclient.Client,
	cluster string, kerberosIDs sets.Set[string]) (map[string][]string, error) {

	users := &userv1.UserList{}
	if err := client.List(ctx, users); err != nil {
		return nil, fmt.Errorf("failed to list users on cluster %s: %w", cluster, err)
	}
	usersWithoutKerberosID := make(map[string][]string)
	for _, user := range users.Items {
		if !kerberosIDs.Has(user.Name) {
			usersWithoutKerberosID[user.Name] = user.Identities
		}
	}
	return usersWithoutKerberosID, nil
}

func makeGroups(openshiftPrivAdmins sets.Set[string], peribolosConfig string, mapping map[string]string, roverGroups map[string][]string, config *group.Config, clusters sets.Set[string]) (map[string]GroupClusters, error) {
	groups := map[string]GroupClusters{}
	var errs []error

	ignoredOpenshiftPrivAdminNames := sets.New[string]()
	if peribolosConfig != "" {
		kerberosIDs := sets.New[string]()
		for _, admin := range sets.List(openshiftPrivAdmins) {
			kerberosID, ok := mapping[admin]
			if !ok {
				if !githubRobotIds.Has(admin) {
					ignoredOpenshiftPrivAdminNames.Insert(admin)
				}
				continue
			}
			kerberosIDs.Insert(kerberosID)
		}
		groups[group.OpenshiftPrivAdminsGroup] = GroupClusters{
			Clusters: sets.New[string](string(api.ClusterAPPCI)),
			Group: &userv1.Group{
				ObjectMeta: metav1.ObjectMeta{Name: group.OpenshiftPrivAdminsGroup, Labels: map[string]string{api.DPTPRequesterLabel: toolName}},
				Users:      sets.List(kerberosIDs),
			},
		}
	}
	if ignoredOpenshiftPrivAdminNames.Len() > 0 {
		logrus.WithField("ignoredOpenshiftPrivAdminNames", sets.List(ignoredOpenshiftPrivAdminNames)).
			Error("These logins are members of openshift-priv but have no mapping to RH login.")
	}

	clustersExceptHive := clusters.Difference(sets.New[string](string(api.HiveCluster)))
	for githubLogin, kerberosId := range mapping {
		groupName := api.GitHubUserGroup(githubLogin)
		groups[groupName] = GroupClusters{
			Clusters: clustersExceptHive,
			Group: &userv1.Group{
				ObjectMeta: metav1.ObjectMeta{Name: groupName, Labels: map[string]string{api.DPTPRequesterLabel: toolName}},
				Users:      []string{kerberosId},
			},
		}
	}

	for k, v := range roverGroups {
		oldGroupName := k
		groupName := k
		clustersForRoverGroup := clusters
		labels := map[string]string{api.DPTPRequesterLabel: toolName}
		if config != nil {
			if v, ok := config.Groups[k]; ok {
				resolved := v.ResolveClusters(config.ClusterGroups)
				if resolved.Len() > 0 {
					logrus.WithField("groupName", groupName).WithField("clusters", sets.List(resolved)).
						Info("Group does not exists on all clusters")
					clustersForRoverGroup = resolved
				}
				if v.RenameTo != "" {
					logrus.WithField("old", oldGroupName).WithField("new", v.RenameTo).
						Info("Group is renamed")
					groupName = v.RenameTo
					labels["rover-group-name"] = oldGroupName
				}
			}
		}
		if _, ok := groups[groupName]; ok {
			errs = append(errs, fmt.Errorf("group %s has been defined already", groupName))
		}
		groups[groupName] = GroupClusters{
			Clusters: clustersForRoverGroup,
			Group: &userv1.Group{
				ObjectMeta: metav1.ObjectMeta{Name: groupName, Labels: labels},
				Users:      sets.List(sets.New[string](v...).Delete("")),
			},
		}
	}
	return groups, kerrors.NewAggregate(errs)
}

func ensureGroups(ctx context.Context, clients map[string]ctrlruntimeclient.Client, groupsToCreate map[string]GroupClusters, maxConcurrency int, dryRun bool, disabledClusters sets.Set[string]) error {
	var errs []error

	for cluster, client := range clients {
		listOption := ctrlruntimeclient.MatchingLabels{
			api.DPTPRequesterLabel: toolName,
		}
		groups := &userv1.GroupList{}
		if err := client.List(ctx, groups, listOption); err != nil {
			errs = append(errs, fmt.Errorf("failed to list groups on cluster %s: %w", cluster, err))
		} else {
			for _, group := range groups.Items {
				var shouldDelete bool
				if groupClusters, ok := groupsToCreate[group.Name]; !ok {
					shouldDelete = true
				} else if !groupClusters.Clusters.Has(cluster) {
					shouldDelete = true
				}
				if shouldDelete {
					if group.Name == api.CIAdminsGroupName {
						// should never happen
						errs = append(errs, fmt.Errorf("attempt to delete group %s on cluster %s", group.Name, cluster))
						continue
					}
					logrus.WithField("cluster", cluster).WithField("group.Name", group.Name).Info("Deleting group ...")
					if dryRun {
						continue
					}
					if err := client.Delete(ctx, &userv1.Group{ObjectMeta: metav1.ObjectMeta{Name: group.Name}}); err != nil && !errors.IsNotFound(err) {
						errs = append(errs, fmt.Errorf("failed to delete group %s on cluster %s: %w", group.Name, cluster, err))
						continue
					}
					logrus.WithField("cluster", cluster).WithField("group.Name", group.Name).Info("Deleted group")
				}
			}
		}
	}

	handleGroup := func(cluster string, client ctrlruntimeclient.Client, group *userv1.Group) error {
		if err := validate(group); err != nil {
			return fmt.Errorf("attempt to create invalid group %s on cluster %s: %w", group.Name, cluster, err)
		}
		logger := logrus.WithFields(logrus.Fields{
			"cluster":    cluster,
			"group.Name": group.Name,
		})
		logger.Info("Upserting group ...")
		if dryRun {
			return nil
		}
		if err := upsertGroupWithRetry(ctx, client, cluster, group, logger); err != nil {
			return fmt.Errorf("failed to upsert group %s on cluster %s after retrying: %w", group.Name, cluster, err)
		}

		return nil
	}

	logrus.WithField("maxConcurrency", maxConcurrency).Info("Set up concurrency")
	errLock := &sync.Mutex{}
	sem := semaphore.NewWeighted(int64(maxConcurrency))
	for _, groupClusters := range groupsToCreate {
		for _, cluster := range sets.List(groupClusters.Clusters) {
			if disabledClusters.Has(cluster) {
				logrus.WithFields(logrus.Fields{"cluster": cluster, "group": groupClusters.Group.Name, "disabledClusters": disabledClusters}).
					Debug("Skipping handling groups for a cluster that is disabled by Prow")
				continue
			}
			group := groupClusters.Group.DeepCopy()
			if err := sem.Acquire(ctx, 1); err != nil {
				return fmt.Errorf("failed to acquire semaphore: %w", err)
			}
			go func(cluster string, client ctrlruntimeclient.Client, group *userv1.Group) {
				defer sem.Release(1)
				if err := handleGroup(cluster, client, group); err != nil {
					errLock.Lock()
					errs = append(errs, err)
					errLock.Unlock()
				}
			}(cluster, clients[cluster], group)
		}
	}

	if err := sem.Acquire(ctx, int64(maxConcurrency)); err != nil {
		logrus.WithError(err).Fatal("failed to acquire semaphore while waiting all workers to finish")
	}

	// For test's stability
	sort.Slice(errs, func(i, j int) bool {
		if errs[i] == nil {
			return true
		}
		if errs[j] == nil {
			return false
		}
		return errs[i].Error() < errs[j].Error()
	})

	return kerrors.NewAggregate(errs)
}

func validate(group *userv1.Group) error {
	if group.Name == "" {
		return fmt.Errorf("group name cannot be empty")
	}
	members := sets.New[string]()
	for _, m := range group.Users {
		if m == "" {
			return fmt.Errorf("member name in group cannot be empty")
		}
		if members.Has(m) {
			return fmt.Errorf("duplicate member: %s", m)
		}
		members.Insert(m)
	}
	return nil
}

func upsertGroupWithRetry(ctx context.Context, client ctrlruntimeclient.Client, cluster string, group *userv1.Group, logger *logrus.Entry) error {
	if err := wait.ExponentialBackoff(wait.Backoff{Steps: 4, Factor: 2, Duration: time.Second}, func() (bool, error) {
		modified, err := upsertGroup(ctx, client, group)
		if err != nil {
			logger.WithError(err).WithField("cluster", cluster).WithField("group", group.Name).Warn("Failed to upsert group")
			return false, nil
		}
		if modified {
			logger.Info("Upserted group (created or modified on the cluster")
			return true, nil
		}
		logger.Info("Group with expected members already present in the cluster")
		return true, nil
	}); err != nil {
		return fmt.Errorf("failed to upsert group %s on cluster %s: %w", group.Name, cluster, err)
	}
	return nil
}

func upsertGroup(ctx context.Context, client ctrlruntimeclient.Client, group *userv1.Group) (modified bool, err error) {
	err = client.Create(ctx, group.DeepCopy())
	if err == nil {
		return true, nil
	}
	if !errors.IsAlreadyExists(err) {
		return false, fmt.Errorf("[1] create failed: %w", err)
	}
	existing := &userv1.Group{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: group.Name}, existing); err != nil {
		return false, fmt.Errorf("[2] get failed: %w", err)
	}
	if equality.Semantic.DeepEqual(group.Users, existing.Users) {
		return false, nil
	}
	if err := client.Delete(ctx, existing); err != nil {
		return false, fmt.Errorf("[3] delete failed: %w", err)
	}
	// Recreate counts as "Update"
	if err := client.Create(ctx, group); err != nil {
		return false, fmt.Errorf("[4] create failed: %w", err)
	}
	return true, nil
}
