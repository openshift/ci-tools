package promotion

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	prowcfg "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

const autoConfigBrancherJob = "periodic-prow-auto-config-brancher"

var currentReleaseArgRE = regexp.MustCompile(`^--current-release=(.+)$`)

var DefaultIgnore = sets.New[string](
	"openshift/gatekeeper",
	"openshift/gatekeeper-operator",
	"openshift-priv/gatekeeper",
	"openshift-priv/gatekeeper-operator",
	"openshift/network.offline_migration_sdn_to_ovnk",
	"openshift-priv/network.offline_migration_sdn_to_ovnk",
	"openshift-pipelines/console-plugin",
	"kubev2v/migration-planner",
	"kubev2v/migration-planner-ui-app",
	"openshift-online/ocm-cluster-service",
)

type prowTideCache struct {
	dir   string
	cache map[string]tideCacheEntry
}

type tideCacheEntry struct {
	tide *prowcfg.Tide
	err  error
}

func newProwTideCache(prowConfigDir string) *prowTideCache {
	if prowConfigDir == "" {
		return nil
	}
	return &prowTideCache{
		dir:   prowConfigDir,
		cache: map[string]tideCacheEntry{},
	}
}

func (c *prowTideCache) loadTide(org, repo string) (*prowcfg.Tide, error) {
	if c == nil {
		return nil, nil
	}
	key := org + "/" + repo
	if e, ok := c.cache[key]; ok {
		return e.tide, e.err
	}
	path := filepath.Join(c.dir, org, repo, "_prowconfig.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			c.cache[key] = tideCacheEntry{tide: nil, err: nil}
			return nil, nil
		}
		wrap := fmt.Errorf("read prow config %s: %w", path, err)
		c.cache[key] = tideCacheEntry{tide: nil, err: wrap}
		return nil, wrap
	}
	var pc prowcfg.ProwConfig
	if err := yaml.Unmarshal(data, &pc); err != nil {
		wrap := fmt.Errorf("unmarshal prow config %s: %w", path, err)
		c.cache[key] = tideCacheEntry{tide: nil, err: wrap}
		return nil, wrap
	}
	c.cache[key] = tideCacheEntry{tide: &pc.Tide, err: nil}
	return &pc.Tide, nil
}

func (c *prowTideCache) hasCurrentReleaseBranchInProw(org, repo, currentRelease string) (bool, error) {
	tide, err := c.loadTide(org, repo)
	if err != nil {
		return false, err
	}
	if tide == nil {
		return false, nil
	}
	return tideIncludesCurrentReleaseBranches(tide, currentRelease), nil
}

func tideIncludesCurrentReleaseBranches(tide *prowcfg.Tide, currentRelease string) bool {
	want := sets.New[string](
		fmt.Sprintf("openshift-%s", currentRelease),
		fmt.Sprintf("release-%s", currentRelease),
	)
	for _, q := range tide.Queries {
		for _, b := range q.IncludedBranches {
			if want.Has(strings.TrimSpace(b)) {
				return true
			}
		}
	}
	return false
}

func parseCurrentReleaseFromInfraPeriodicsData(data []byte) (string, error) {
	var doc struct {
		Periodics []prowcfg.Periodic `json:"periodics"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	for _, job := range doc.Periodics {
		if job.Name != autoConfigBrancherJob {
			continue
		}
		if job.Spec == nil {
			continue
		}
		for _, c := range job.Spec.Containers {
			for _, arg := range c.Args {
				if m := currentReleaseArgRE.FindStringSubmatch(strings.TrimSpace(arg)); len(m) > 1 {
					return strings.TrimSpace(m[1]), nil
				}
			}
		}
	}
	return "", fmt.Errorf("no --current-release found in %s job", autoConfigBrancherJob)
}

func getCurrentReleaseFromInfraPeriodics(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	release, err := parseCurrentReleaseFromInfraPeriodicsData(data)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return release, nil
}

func isMainOrMasterBranch(branch string) bool {
	return branch == "main" || branch == "master"
}

func isReleaseBranch(branch, currentRelease string) bool {
	return branch == fmt.Sprintf("release-%s", currentRelease) || branch == fmt.Sprintf("openshift-%s", currentRelease)
}

func isPromotionFullyDisabled(cfg *cioperatorapi.ReleaseBuildConfiguration) bool {
	if cfg.PromotionConfiguration == nil || len(cfg.PromotionConfiguration.Targets) == 0 {
		return true
	}
	for _, t := range cfg.PromotionConfiguration.Targets {
		if !t.Disabled {
			return false
		}
	}
	return true
}

type options struct {
	configDir           string
	currentRelease      string
	prowTideCache       *prowTideCache
	infraPeriodicsPath  string
	ignore              sets.Set[string]
	reposWithMainMaster sets.Set[string]
}

func (o *options) resolveCurrentRelease() (string, error) {
	if o.currentRelease != "" {
		return o.currentRelease, nil
	}
	if o.infraPeriodicsPath == "" {
		return "", fmt.Errorf("either --current-release or --infra-periodics is required for main-promotion validation")
	}
	return getCurrentReleaseFromInfraPeriodics(o.infraPeriodicsPath)
}

func (o *options) collectReposWithMainMaster() error {
	return config.OperateOnCIOperatorConfigDir(o.configDir, func(cfg *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
		if isMainOrMasterBranch(info.Branch) {
			o.reposWithMainMaster.Insert(fmt.Sprintf("%s/%s", info.Org, info.Repo))
		}
		return nil
	})
}

func Validate(configDir, currentRelease, prowConfigDir, infraPeriodicsPath string, ignore sets.Set[string]) error {
	if ignore == nil {
		ignore = sets.New[string]()
	}
	ignore = ignore.Union(DefaultIgnore)

	o := &options{
		configDir:           configDir,
		currentRelease:      currentRelease,
		prowTideCache:       newProwTideCache(prowConfigDir),
		infraPeriodicsPath:  infraPeriodicsPath,
		ignore:              ignore,
		reposWithMainMaster: sets.New[string](),
	}
	release, err := o.resolveCurrentRelease()
	if err != nil {
		return err
	}
	releasePriv := release + "-priv"

	if err := o.collectReposWithMainMaster(); err != nil {
		return err
	}

	var errs []error
	if err := config.OperateOnCIOperatorConfigDir(configDir, func(cfg *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
		orgRepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
		relPath := filepath.Join(info.Org, info.Repo, filepath.Base(info.Filename))

		if ignore.Has(orgRepo) {
			return nil
		}

		if isMainOrMasterBranch(info.Branch) {
			if cfg.PromotionConfiguration == nil {
				return nil
			}
			for _, t := range cfg.PromotionConfiguration.Targets {
				if t.Disabled {
					continue
				}
				ns := t.Namespace
				if ns == "" {
					ns = "ocp"
				}
				name := strings.Trim(t.Name, `"`)
				switch ns {
				case "ocp":
					if name != release {
						errs = append(errs, fmt.Errorf("%s: promotes to %s/%s (main/master must only promote to %s)", relPath, ns, name, release))
					}
				case "ocp-private":
					if name != releasePriv {
						errs = append(errs, fmt.Errorf("%s: promotes to %s/%s (main/master must only promote to %s)", relPath, ns, name, releasePriv))
					}
				default:
					errs = append(errs, fmt.Errorf("%s: promotes to %s/%s (main/master may only promote to ocp/%s or ocp-private/%s)", relPath, ns, name, release, releasePriv))
				}
			}
			return nil
		}

		if isReleaseBranch(info.Branch, release) && o.reposWithMainMaster.Has(orgRepo) {
			inScopeProw := false
			if o.prowTideCache != nil {
				var perr error
				inScopeProw, perr = o.prowTideCache.hasCurrentReleaseBranchInProw(info.Org, info.Repo, release)
				if perr != nil {
					return perr
				}
			}
			if !inScopeProw {
				return nil
			}
			if !isPromotionFullyDisabled(cfg) {
				errs = append(errs, fmt.Errorf("%s: release/openshift-%s config must have promotion disabled (only main/master promote to %s)", relPath, release, release))
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if len(errs) > 0 {
		for _, e := range errs {
			logrus.Error(e.Error())
		}
		return fmt.Errorf("main promotion validation failed: %d violation(s)", len(errs))
	}
	return nil
}
