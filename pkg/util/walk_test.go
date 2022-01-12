package util

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func createDir(t *testing.T, dirs ...string) string {
	ret := t.TempDir()
	for _, x := range dirs {
		if err := os.MkdirAll(filepath.Join(ret, x), 0777); err != nil {
			t.Fatal(err)
		}
	}
	return ret
}

func TestWalkFD(t *testing.T) {
	check := func(d, f string, err error, names, notFound *[]string) {
		if r, err := filepath.Rel(d, f); err == nil {
			f = r
		} else {
			t.Fatal(err)
		}
		if err != nil {
			if os.IsNotExist(err) {
				*notFound = append(*notFound, f)
			}
			return
		}
		*names = append(*names, f)
		if filepath.Base(f) == "0" {
			if err := os.Rename(d, d+".old"); err != nil {
				t.Error(err)
			}
		}
	}
	d0 := createDir(t, "0", "1", "2")
	var names, notFound []string
	err := filepath.WalkDir(d0, func(f string, _ fs.DirEntry, err error) error {
		check(d0, f, err, &names, &notFound)
		return nil
	})
	if err != nil {
		t.Error(err)
	}
	testhelper.Diff(t, "names", names, []string{".", "0", "1", "2"})
	testhelper.Diff(t, "not found", notFound, []string{"0", "1", "2"})
	d1 := createDir(t, "0", "1", "2")
	f, err := os.Open(d1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	names, notFound = nil, nil
	err = WalkFD(f, os.O_RDONLY, 0777, func(f *os.File, _ fs.DirEntry, err error) error {
		check(d1, f.Name(), err, &names, &notFound)
		return nil
	})
	if err != nil {
		t.Error(err)
	}
	testhelper.Diff(t, "names", names, []string{".", "0", "1", "2"})
	testhelper.Diff(t, "not found", notFound, []string(nil))
}

// TestWalkFDErrors verifies correct behavior and propagation when errors occur.
func TestWalkFDErrors(t *testing.T) {
	defaultStart := func(dir string) *os.File {
		ret, err := os.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		return ret
	}
	removeRoot := func(dir string) *os.File {
		ret := defaultStart(dir)
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		return ret
	}
	for _, tc := range []struct {
		name    string
		dirs    []string
		start   func(string) *os.File
		fn      func(string, *os.File, fs.DirEntry, error) error
		visited func(string) []string
		errs    func(string) []string
	}{{
		name:  "root not found",
		start: removeRoot,
		visited: func(dir string) []string {
			return []string{
				dir, // `stat(2)` never fails on an open FD in Linux
				dir, // subsequent error reported for `readdir(2)`
			}
		},
		errs: func(dir string) []string { return []string{filepath.Base(dir)} },
	}, {
		name: "child directory removed",
		dirs: []string{"child0", "child1"},
		fn: func(dir string, f *os.File, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Name() == "child0" {
				if err := os.Remove(filepath.Join(dir, "child1")); err != nil {
					t.Fatal(err)
				}
			}
			return nil
		},
		visited: func(dir string) []string {
			return []string{dir, "child0", "child1"}
		},
		errs: func(string) []string { return []string{"child1"} },
	}, {
		name: "error propagated",
		fn: func(dir string, f *os.File, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return errors.New("injected error")
		},
		visited: func(dir string) []string { return []string{dir} },
		errs:    func(string) []string { return nil },
	}, {
		name:  "error ignored",
		start: removeRoot,
		fn: func(_ string, _ *os.File, _ fs.DirEntry, err error) error {
			return nil
		},
		visited: func(dir string) []string { return []string{dir, dir} },
	}, {
		name: "child error ignored",
		dirs: []string{"child0", "child1"},
		fn: func(dir string, f *os.File, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					err = nil
				}
				return err
			}
			if d.Name() == "child0" {
				if err := os.Remove(filepath.Join(dir, "child1")); err != nil {
					t.Fatal(err)
				}
			}
			return nil
		},
		visited: func(dir string) []string {
			return []string{dir, "child0", "child1"}
		},
	}, {
		name: "SkipDir",
		dirs: []string{"child0/child00", "child0/child01"},
		fn: func(dir string, f *os.File, d fs.DirEntry, err error) error {
			if err == nil && d.Name() == "child0" {
				return fs.SkipDir
			}
			return err
		},
		visited: func(dir string) []string { return []string{dir, "child0"} },
	}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := createDir(t, tc.dirs...)
			var f *os.File
			if tc.start == nil {
				f = defaultStart(dir)
			} else {
				f = tc.start(dir)
			}
			var visited []string
			var notFound []string
			if err := WalkFD(f, 0, 0, func(f *os.File, d fs.DirEntry, err error) error {
				visited = append(visited, d.Name())
				if err != nil && os.IsNotExist(err) {
					notFound = append(notFound, d.Name())
				}
				if tc.fn != nil {
					return tc.fn(dir, f, d, err)
				} else {
					return err
				}
			}); tc.errs == nil {
				if err != nil {
					t.Errorf("expected no error, got %q", err)
				}
			} else if err == nil {
				t.Error("expected error, got none")
			} else {
				testhelper.Diff(t, `"not found" errors`, notFound, tc.errs(dir))
			}
			testhelper.Diff(t, "visited files", visited, tc.visited(filepath.Base(dir)))
		})
	}
}
