package bumper

import (
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/util"
)

type Bumper[T any] interface {
	GetFiles() ([]string, error)

	Unmarshall(file string) (T, error)

	BumpFilename(filename string, obj T) (string, error)

	BumpContent(obj T) (T, error)

	Marshall(obj T, bumpedFilename, dir string) error
}

type BumpingOptions struct {
	OutDir string
}

// Bump bumps files using the Bumpers b according to the BumpingOptions.
func Bump[T any](b Bumper[T], o *BumpingOptions) error {
	filesCh := make(chan string)
	produce := func() error {
		defer close(filesCh)
		files, err := b.GetFiles()
		logrus.Debugf("files: %+v", files)
		if err != nil {
			return err
		}
		for _, f := range files {
			filesCh <- f
		}
		return nil
	}
	errsChan := make(chan error)
	map_ := func() error {
		for f := range filesCh {
			if err := BumpObject(b, f, o.OutDir); err != nil {
				errsChan <- err
			}
		}
		return nil
	}
	return util.ProduceMap(0, produce, map_, errsChan)
}

func BumpObject[T any](b Bumper[T], file, outDir string) error {
	logrus.Infof("bumping config %s", file)

	srcFileFullPath := file

	obj, err := b.Unmarshall(srcFileFullPath)
	if err != nil {
		logrus.WithError(err).Errorf("failed to unmarshall file %s", srcFileFullPath)
		return fmt.Errorf("unmarshall file %s: %w", file, err)
	}

	filename := path.Base(file)

	logrus.Infof("bumping filename %s", filename)
	bumpedFilename, err := b.BumpFilename(filename, obj)
	if err != nil {
		logrus.WithError(err).Errorf("error bumping file %s", bumpedFilename)
		return fmt.Errorf("bump filename: %w", err)
	}
	logrus.Infof("bumped filename %s", bumpedFilename)

	outDir = getOutDir(file, outDir)
	logrus.Debugf("out dir: %s", outDir)
	dstFileFullPath := path.Join(outDir, bumpedFilename)

	if _, err := os.Stat(dstFileFullPath); err == nil {
		logrus.Warnf("file %s already exists, skipping", dstFileFullPath)
		return fmt.Errorf("file %s already exists", dstFileFullPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("file exists: %w", err)
	}

	logrus.Infof("bumping obj")
	bumpedObj, err := b.BumpContent(obj)
	if err != nil {
		logrus.WithError(err).Error("error bumping obj")
		return fmt.Errorf("bump object: %w", err)
	}

	logrus.Infof("marshalling obj %s to %s", bumpedFilename, outDir)
	if err := b.Marshall(bumpedObj, bumpedFilename, outDir); err != nil {
		logrus.WithError(err).Error("error marshalling obj")
		return fmt.Errorf("marshall obj: %w", err)
	}

	return nil
}

func getOutDir(file string, dir string) string {
	if dir != "" {
		return dir
	}
	return path.Dir(file)
}
