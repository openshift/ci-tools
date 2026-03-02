package rehearse

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"

	"github.com/mattn/go-zglob"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kutilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	prowgithub "sigs.k8s.io/prow/pkg/github"
	_ "sigs.k8s.io/prow/pkg/hook"
	prowplugins "sigs.k8s.io/prow/pkg/plugins"
	"sigs.k8s.io/prow/pkg/plugins/updateconfig"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const (
	createByRehearse  = "created-by-pj-rehearse"
	rehearseLabelPull = "ci.openshift.org/rehearse-pull"
)

// ConfigMaps holds the data about the ConfigMaps affected by a rehearse run
type ConfigMaps struct {
	// Paths is a set of repo paths that changed content and belong to some ConfigMap
	Paths sets.Set[string]
	// Names is a mapping from production ConfigMap names to rehearse-specific ones
	Names map[string]string
	// ProductionNames is a set of production ConfigMap names
	ProductionNames sets.Set[string]
	// Patterns is the set of config-updater patterns that cover at least one changed file
	Patterns sets.Set[string]
}

// NewConfigMaps populates a ConfigMaps instance
func NewConfigMaps(paths []string, purpose, SHA string, prNumber int, configUpdaterCfg prowplugins.ConfigUpdater) (ConfigMaps, error) {
	cms := ConfigMaps{
		Paths:           sets.New[string](paths...),
		Names:           nil,
		ProductionNames: sets.New[string](),
		Patterns:        sets.New[string](),
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
		cms.Names[cmName] = tempConfigMapName(purpose, cmName, SHA, prNumber)
		cms.ProductionNames.Insert(cmName)
		cms.Patterns.Insert(pattern)
	}

	return cms, kutilerrors.NewAggregate(errs)
}

func tempConfigMapName(purpose, source, SHA string, prNumber int) string {
	// Object names can't be too long so we truncate the hash. This increases
	// chances of collision but we can tolerate it as our input space is tiny.
	pr := strconv.Itoa(prNumber)
	maxLen := 253 - len("rehearse----") - len(purpose) - len(pr) - len(SHA)
	if len(source) > maxLen {
		source = source[:maxLen]
	}
	return fmt.Sprintf("rehearse-%s-%s-%s-%s", pr, SHA, purpose, source)
}

// CMManager manages temporary ConfigMaps created on build clusters to be
// consumed by rehearsals. This is necessary when a content of a ConfigMap, such
// as a template or cluster profile, is changed in a pull request. In such case
// the rehearsals that use that ConfigMap must have access to the updated content.
type CMManager struct {
	cluster, namespace string
	cmclient           corev1.ConfigMapInterface
	configUpdaterCfg   prowplugins.ConfigUpdater
	prNumber           int
	releaseRepoPath    string
	logger             *logrus.Entry
}

// NewCMManager creates a new CMManager
func NewCMManager(
	cluster, namespace string,
	cmclient corev1.ConfigMapInterface,
	configUpdaterCfg prowplugins.ConfigUpdater,
	prNumber int,
	releaseRepoPath string,
	logger *logrus.Entry,
) *CMManager {
	return &CMManager{
		cluster:          cluster,
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
	return gzip.ReadFileMaybeGZIP(filepath.Join(g.root, filename))
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
	} else if err := updateconfig.Update(osFileGetter{root: c.releaseRepoPath}, c.cmclient, cm.Name, "", data, true, nil, c.logger, ""); err != nil {
		return err
	}
	return nil
}

func genChanges(root string, patterns sets.Set[string]) ([]prowgithub.PullRequestChange, error) {
	var ret []prowgithub.PullRequestChange
	err := filepath.WalkDir(root, func(path string, info fs.DirEntry, err error) error {
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

func replaceSpecNames(cluster, namespace string, cfg prowplugins.ConfigUpdater, mapping map[string]string) (ret prowplugins.ConfigUpdater) {
	ret = cfg
	ret.Maps = make(map[string]prowplugins.ConfigMapSpec, len(cfg.Maps))
	for k, v := range cfg.Maps {
		if namespaces, configured := v.Clusters[cluster]; !configured || !sets.New[string](namespaces...).Has(namespace) {
			continue
		}
		if name, ok := mapping[v.Name]; ok {
			v.Name = name
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
	for cm, data := range updateconfig.FilterChanges(replaceSpecNames(c.cluster, c.namespace, c.configUpdaterCfg, cms.Names), changes, c.namespace, false, c.logger, "") {
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
