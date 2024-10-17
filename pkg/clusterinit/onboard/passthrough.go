package onboard

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/ryanuber/go-glob"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

const (
	passthroughRoot string = "manifests"
)

//go:embed manifests
var manifests embed.FS

type passthroughstep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
	manifests      fs.FS
	readFile       func(fsys fs.FS, name string) ([]byte, error)
	writeFile      func(name string, data []byte, perm fs.FileMode) error
	mkdirAll       func(path string, perm fs.FileMode) error
}

func (s *passthroughstep) Name() string {
	return "passthrough-manifests"
}

func (s *passthroughstep) Run(ctx context.Context) error {
	log := s.log.WithField("step", s.Name())

	if s.clusterInstall.Onboard.PassthroughManifest.Skip {
		log.Info("step is not enabled, skipping")
		return nil
	}

	clusterRoot := BuildFarmDirFor(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	subFS, err := fs.Sub(s.manifests, passthroughRoot)
	if err != nil {
		return fmt.Errorf("subfs: %w", err)
	}
	if err := fs.WalkDir(subFS, ".", func(p string, d fs.DirEntry, _ error) error {
		if p == "." {
			return nil
		}

		if g, exclude := s.excludePath(p); exclude {
			log.WithField("path", p).WithField("pattern", g).Info("exclude path")
			return nil
		}

		fullPath := path.Join(clusterRoot, p)
		if d.IsDir() {
			log.WithField("dir", fullPath).Info("creating directory")
			return s.mkdirAll(fullPath, 0755)
		}

		data, err := s.readFile(manifests, p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}

		log.WithField("file", fullPath).Info("copying file")
		return s.writeFile(fullPath, data, 0644)
	}); err != nil {
		return fmt.Errorf("create manifests: %w", err)
	}

	return nil
}

func (s *passthroughstep) excludePath(path string) (string, bool) {
	for _, g := range s.clusterInstall.Onboard.PassthroughManifest.Exclude {
		if glob.Glob(g, path) {
			return g, true
		}
	}
	return "", false
}

func NewPassthroughStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *passthroughstep {
	return &passthroughstep{
		log:            log,
		clusterInstall: clusterInstall,
		manifests:      manifests,
		readFile:       fs.ReadFile,
		writeFile:      os.WriteFile,
		mkdirAll:       os.MkdirAll,
	}
}
