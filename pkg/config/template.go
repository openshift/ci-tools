package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mattn/go-zglob"
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
	PathInRepo, SHA string
}

func (s ConfigMapSource) Name() string {
	base := filepath.Base(s.PathInRepo)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func (s ConfigMapSource) CMName(prefix string) string {
	return prefix + s.Name()
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

// RehearsalCMManager holds the details needed for the configmap controller
type RehearsalCMManager struct {
	namespace        string
	cmclient         corev1.ConfigMapInterface
	configUpdaterCfg prowplugins.ConfigUpdater
	prNumber         int
	releaseRepoPath  string
	logger           *logrus.Entry
}

// NewRehearsalCMManager creates a new RehearsalCMManager
func NewRehearsalCMManager(
	namespace string,
	cmclient corev1.ConfigMapInterface,
	configUpdaterCfg prowplugins.ConfigUpdater,
	prNumber int,
	releaseRepoPath string,
	logger *logrus.Entry,
) *RehearsalCMManager {
	return &RehearsalCMManager{
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

func (c *RehearsalCMManager) createCM(name string, data []updateconfig.ConfigMapUpdate) error {
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

func genChanges(root string, sources []ConfigMapSource) ([]prowgithub.PullRequestChange, error) {
	var ret []prowgithub.PullRequestChange
	for _, f := range sources {
		err := filepath.Walk(filepath.Join(root, f.PathInRepo), func(path string, info os.FileInfo, err error) error {
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

func (c *RehearsalCMManager) validateChanges(changes []prowgithub.PullRequestChange) error {
	var errs []error
	for _, change := range changes {
		found := false
		for glob := range c.configUpdaterCfg.Maps {
			var err error
			if found, err = zglob.Match(glob, change.Filename); err != nil {
				errs = append(errs, err)
			} else if found {
				break
			}
		}
		if !found {
			errs = append(errs, fmt.Errorf("no entry in `updateconfig` matches %q", change.Filename))
		}
	}
	return kutilerrors.NewAggregate(errs)
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

func (c *RehearsalCMManager) createCMs(sources []ConfigMapSource, mapping map[string]string) error {
	changes, err := genChanges(c.releaseRepoPath, sources)
	if err != nil {
		return err
	}
	if err := c.validateChanges(changes); err != nil {
		return err
	}
	var errs []error
	for cm, data := range updateconfig.FilterChanges(replaceSpecNames(c.namespace, c.configUpdaterCfg, mapping), changes, c.namespace, c.logger) {
		c.logger.WithFields(logrus.Fields{"cm-name": cm.Name}).Info("creating rehearsal configMap")
		if err := c.createCM(cm.Name, data); err != nil {
			errs = append(errs, err)
		}
	}
	return kutilerrors.NewAggregate(errs)
}

// CreateCMTemplates creates configMaps for all the changed templates.
func (c *RehearsalCMManager) CreateCMTemplates(templates []ConfigMapSource) error {
	nameMap := make(map[string]string, len(templates))
	for _, t := range templates {
		nameMap[t.CMName(TemplatePrefix)] = t.TempCMName("template")
	}
	return c.createCMs(templates, nameMap)
}

func (c *RehearsalCMManager) CreateClusterProfiles(profiles []ConfigMapSource) error {
	nameMap := make(map[string]string, len(profiles))
	for _, p := range profiles {
		nameMap[p.CMName(ClusterProfilePrefix)] = p.TempCMName("cluster-profile")
	}
	return c.createCMs(profiles, nameMap)
}

// CleanupCMTemplates deletes all the configMaps that have been created for the changed templates.
func (c *RehearsalCMManager) CleanupCMTemplates() error {
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
