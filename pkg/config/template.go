package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

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
)

type ConfigMapSource struct {
	Filename, SHA string
}

func (s ConfigMapSource) Name() string {
	base := filepath.Base(s.Filename)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func (p ConfigMapSource) CMName(prefix string) string {
	return prefix + p.Name()
}

func (s ConfigMapSource) TempCMName(prefix string) string {
	// Object names can't be too long so we truncate the hash. This increases
	// chances of collision but we can tolerate it as our input space is tiny.
	return fmt.Sprintf("rehearse-%s-%s-%s", prefix, s.Name(), s.SHA[:8])
}

const (
	createByRehearse  = "created-by-pj-rehearse"
	rehearseLabelPull = "ci.openshift.org/rehearse-pull"
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
	} else if err := updateconfig.Update(osFileGetter{root: c.releaseRepoPath}, c.cmclient, cm.Name, "", data, nil, c.logger); err != nil {
		return err
	}
	return nil
}

func genChanges(root string, sources []ConfigMapSource) ([]prowgithub.PullRequestChange, error) {
	var ret []prowgithub.PullRequestChange
	for _, f := range sources {
		err := filepath.Walk(filepath.Join(root, f.Filename), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			// Failure is impossible per filepath.Walk's API.
			path, err = filepath.Rel(root, path)
			if err != nil {
				return err
			}
			ret = append(ret, prowgithub.PullRequestChange{
				Filename: path,
				Status:   string(prowgithub.PullRequestFileModified),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
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
		}
		v.Namespaces = []string{""}
		ret.Maps[k] = v
	}
	return
}

func (c *TemplateCMManager) createCMs(sources []ConfigMapSource, mapping map[string]string) error {
	changes, err := genChanges(c.releaseRepoPath, sources)
	if err != nil {
		return err
	}
	var errs []error
	for cm, data := range updateconfig.FilterChanges(replaceSpecNames(c.namespace, c.configUpdaterCfg, mapping), changes, c.logger) {
		c.logger.WithFields(logrus.Fields{"cm-name": cm.Name}).Info("creating rehearsal configMap")
		if err := c.createCM(cm.Name, data); err != nil {
			errs = append(errs, err)
		}
	}
	return kutilerrors.NewAggregate(errs)
}

// CreateCMTemplates creates configMaps for all the changed templates.
func (c *TemplateCMManager) CreateCMTemplates(templates []ConfigMapSource) error {
	nameMap := make(map[string]string, len(templates))
	for _, t := range templates {
		nameMap[t.CMName(TemplatePrefix)] = t.TempCMName("template")
	}
	return c.createCMs(templates, nameMap)
}

func (c *TemplateCMManager) CreateClusterProfiles(profiles []ConfigMapSource) error {
	nameMap := make(map[string]string, len(profiles))
	for _, p := range profiles {
		nameMap[p.CMName(ClusterProfilePrefix)] = p.TempCMName("cluster-profile")
	}
	return c.createCMs(profiles, nameMap)
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
