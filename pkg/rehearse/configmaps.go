package rehearse

import (
	"context"
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

const (
	createByRehearse  = "created-by-pj-rehearse"
	rehearseLabelPull = "ci.openshift.org/rehearse-pull"
)

// ConfigMaps holds the data about the ConfigMaps affected by a rehearse run
type ConfigMaps struct {
	// Paths is a set of repo paths that changed content and belong to some ConfigMap
	Paths sets.String
	// Names is a mapping from production ConfigMap names to rehearse-specific ones
	Names map[string]string
	// ProductionNames is a set of production ConfigMap names
	ProductionNames sets.String
	// Patterns is the set of config-updater patterns that cover at least one changed file
	Patterns sets.String
}

// NewConfigMaps populates a ConfigMaps instance
func NewConfigMaps(paths []string, purpose, buildId string, prNumber int, configUpdaterCfg prowplugins.ConfigUpdater) (ConfigMaps, error) {
	cms := ConfigMaps{
		Paths:           sets.NewString(paths...),
		Names:           nil,
		ProductionNames: sets.NewString(),
		Patterns:        sets.NewString(),
	}

	var errs []error
	for _, path := range paths {
		cmName, pattern, err := config.ConfigMapName(path, configUpdaterCfg)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if cms.Names == nil {
			cms.Names = make(map[string]string)
		}
		cms.Names[cmName] = tempConfigMapName(purpose, cmName, buildId, prNumber)
		cms.ProductionNames.Insert(cmName)
		cms.Patterns.Insert(pattern)
	}

	return cms, kutilerrors.NewAggregate(errs)
}

func tempConfigMapName(purpose, source, buildId string, prNumber int) string {
	// Object names can't be too long so we truncate the hash. This increases
	// chances of collision but we can tolerate it as our input space is tiny.
	pr := strconv.Itoa(prNumber)
	maxLen := 253 - len("rehearse----") - len(purpose) - len(pr) - len(buildId)
	if len(source) > maxLen {
		source = source[:maxLen]
	}
	return fmt.Sprintf("rehearse-%s-%s-%s-%s", pr, buildId, purpose, source)
}

// CMManager manages temporary ConfigMaps created on build clusters to be
// consumed by rehearsals. This is necessary when a content of a ConfigMap, such
// as a template or cluster profile, is changed in a pull request. In such case
// the rehearsals that use that ConfigMap must have access to the updated content.
type CMManager struct {
	namespace        string
	cmclient         corev1.ConfigMapInterface
	configUpdaterCfg prowplugins.ConfigUpdater
	prNumber         int
	releaseRepoPath  string
	logger           *logrus.Entry
}

// NewCMManager creates a new CMManager
func NewCMManager(
	namespace string,
	cmclient corev1.ConfigMapInterface,
	configUpdaterCfg prowplugins.ConfigUpdater,
	prNumber int,
	releaseRepoPath string,
	logger *logrus.Entry,
) *CMManager {
	return &CMManager{
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

func (c *CMManager) createCM(name string, data []updateconfig.ConfigMapUpdate) error {
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
	if _, err := c.cmclient.Create(context.TODO(), cm, metav1.CreateOptions{}); err != nil && !kerrors.IsAlreadyExists(err) {
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

	return ret, err
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

func (c *CMManager) Create(cms ConfigMaps) error {
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

// Clean deletes all the configMaps that have been created for this PR
func (c *CMManager) Clean() error {
	c.logger.Info("deleting temporary template configMaps")
	if err := c.cmclient.DeleteCollection(context.TODO(),
		metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: fields.Set{
			createByRehearse:  "true",
			rehearseLabelPull: strconv.Itoa(c.prNumber),
		}.AsSelector().String()}); err != nil {
		return err
	}
	return nil
}
