package testhelper

import (
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TmpDir(t *testing.T, files map[string]fstest.MapFile) (string, error) {
	ret := t.TempDir()
	t.Cleanup(func() {
		os.RemoveAll(ret)
	})
	for k, v := range files {
		dir := filepath.Join(ret, filepath.Dir(k))
		if err := os.MkdirAll(dir, fs.ModePerm); err != nil {
			return "", fmt.Errorf("failed to create directory: %w", err)
		}
		if err := ioutil.WriteFile(filepath.Join(ret, k), v.Data, v.Mode); err != nil {
			return "", fmt.Errorf("failed to create file: %w", err)
		}
	}
	return ret, nil
}
