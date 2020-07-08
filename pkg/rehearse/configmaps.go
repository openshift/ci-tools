package rehearse

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mattn/go-zglob"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/api/core/v1"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kutilerrors "k8s.io/apimachinery/pkg/util/errors"

	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	prowgithub "k8s.io/test-infra/prow/github"
	_ "k8s.io/test-infra/prow/hook"
	prowplugins "k8s.io/test-infra/prow/plugins"
	"k8s.io/test-infra/prow/plugins/updateconfig"

	"github.com/openshift/ci-tools/pkg/config"
)

type RehearsalConfigMaps struct {
	// Names is a mapping from production ConfigMap names to rehearsal names
	Names map[string]string
	// Patterns is the set of config-updater patterns that cover at least one changed file
	Patterns sets.String
}

func NewRehearsalConfigMaps(sources []config.ConfigMapSource, purpose string, configUpdaterCfg prowplugins.ConfigUpdater) (RehearsalConfigMaps, error) {
	cms := RehearsalConfigMaps{
		Names:    map[string]string{},
		Patterns: sets.NewString(),
	}

	var errs []error
	for _, source := range sources {
		cmName, pattern, err := source.CMName(configUpdaterCfg)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		cms.Names[cmName] = RehearsalCMName(purpose, cmName, source.SHA)
		cms.Patterns.Insert(pattern)
	}
	return cms, kutilerrors.NewAggregate(errs)
}

func RehearsalCMName(purpose, source, SHA string) string {
	// Object names can't be too long so we truncate the hash. This increases
	// chances of collision but we can tolerate it as our input space is tiny.
	maxLen := 253 - len("rehearse") - len(purpose) - 8 - 3 // SHA fragment + 3 separators
	if len(source) > maxLen {
		source = source[:maxLen]
	}
	return fmt.Sprintf("rehearse-%s-%s-%s", purpose, source, SHA[:8])
}

const (
	createByRehearse  = "created-by-pj-rehearse"
	rehearseLabelPull = "ci.openshift.org/rehearse-pull"

	// ClusterProfilePrefix is the prefix added to ConfigMap names
	ClusterProfilePrefix = "cluster-profile-"
)

// TemplateCMManager holds the details needed for the configmap controller
type TemplateCMManager struct {
	namespace        string
	cmclient         corev1.ConfigMapInterface
	configUpdaterCfg prowplugins.ConfigUpdater
	prNumber         int
	releaseRepoPath  string
	logger           *logrus.Entry
}

// NewTemplateCMManager creates a new TemplateCMManager
func NewTemplateCMManager(
	namespace string,
	cmclient corev1.ConfigMapInterface,
	configUpdaterCfg prowplugins.ConfigUpdater,
	prNumber int,
	releaseRepoPath string,
	logger *logrus.Entry,
) *TemplateCMManager {
	return &TemplateCMManager{
		namespace:        namespace,
		cmclient:         cmclient,
		configUpdaterCfg: configUpdaterCfg,
		prNumber:         prNumber,
		releaseRepoPath:  releaseRepoPath,
		logger:           logger,
	}
}

type osFileGetter struct {
	root string
}

func (g osFileGetter) GetFile(filename string) ([]byte, error) {
	return ioutil.ReadFile(filepath.Join(g.root, filename))
}

func (c *TemplateCMManager) createCM(name string, data []updateconfig.ConfigMapUpdate) error {
	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				createByRehearse:  "true",
				rehearseLabelPull: strconv.Itoa(c.prNumber),
			},
		},
		Data: map[string]string{},
	}
	if _, err := c.cmclient.Create(cm); err != nil && !kerrors.IsAlreadyExists(err) {
		return err
	} else if err := updateconfig.Update(osFileGetter{root: c.releaseRepoPath}, c.cmclient, cm.Name, "", data, true, nil, c.logger); err != nil {
		return err
	}
	return nil
}

func genChanges(root string, patterns sets.String) ([]prowgithub.PullRequestChange, error) {
	var ret []prowgithub.PullRequestChange
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		// Failure is impossible per filepath.Walk's API.
		path, err = filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for pattern := range patterns {
			match, err := zglob.Match(pattern, path)
			if err != nil {
				return err
			}
			if match {
				ret = append(ret, prowgithub.PullRequestChange{
					Filename: path,
					Status:   string(prowgithub.PullRequestFileModified),
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func replaceSpecNames(namespace string, cfg prowplugins.ConfigUpdater, mapping map[string]string) (ret prowplugins.ConfigUpdater) {
	ret = cfg
	ret.Maps = make(map[string]prowplugins.ConfigMapSpec, len(cfg.Maps))
	for k, v := range cfg.Maps {
		if v.Namespaces[0] != "" && v.Namespaces[0] != namespace {
			continue
		}
		if name, ok := mapping[v.Name]; ok {
			v.Name = name
			v.Namespaces = []string{""}
			ret.Maps[k] = v
		}
	}
	return
}

func (c *TemplateCMManager) CreateCMs(cms RehearsalConfigMaps) error {
	changes, err := genChanges(c.releaseRepoPath, cms.Patterns)
	if err != nil {
		return err
	}

	var errs []error
	for cm, data := range updateconfig.FilterChanges(replaceSpecNames(c.namespace, c.configUpdaterCfg, cms.Names), changes, c.namespace, c.logger) {
		c.logger.WithFields(logrus.Fields{"cm-name": cm.Name}).Info("creating rehearsal configMap")
		if err := c.createCM(cm.Name, data); err != nil {
			errs = append(errs, err)
		}
	}
	return kutilerrors.NewAggregate(errs)
}

// CleanupCMTemplates deletes all the configMaps that have been created for the changed templates.
func (c *TemplateCMManager) CleanupCMTemplates() error {
	c.logger.Info("deleting temporary template configMaps")
	if err := c.cmclient.DeleteCollection(&metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: fields.Set{
			createByRehearse:  "true",
			rehearseLabelPull: strconv.Itoa(c.prNumber),
		}.AsSelector().String()}); err != nil {
		return err
	}
	return nil
}
