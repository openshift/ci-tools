package util

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
)

// Silly hack from the stdlib.
// https://go.googlesource.com/go.git/+/refs/tags/go1.17.6/src/io/fs/walk.go#120
type statDirEntry struct {
	info fs.FileInfo
}

func (d *statDirEntry) Name() string               { return d.info.Name() }
func (d *statDirEntry) IsDir() bool                { return d.info.IsDir() }
func (d *statDirEntry) Type() fs.FileMode          { return d.info.Mode().Type() }
func (d *statDirEntry) Info() (fs.FileInfo, error) { return d.info, nil }

type WalkFDFn func(f *os.File, info fs.DirEntry, err error) error

func walkFD(f *os.File, d fs.DirEntry, flags int, mode uint32, err error, fn WalkFDFn) error {
	if !d.IsDir() {
		return fn(f, d, err)
	}
	var dup *os.File
	if err == nil {
		var fd int
		if fd, err = unix.Dup(int(f.Fd())); err == nil {
			dup = os.NewFile(uintptr(fd), f.Name())
			defer dup.Close()
		}
	}
	if errFn := fn(f, d, err); err != nil || errFn != nil {
		if errFn == fs.SkipDir {
			errFn = nil
		}
		return errFn
	}
	dirs, err := dup.ReadDir(-1)
	if err != nil {
		err = fn(f, d, err)
		if err != nil {
			return err
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].Name() < dirs[j].Name()
	})
	for _, d1 := range dirs {
		fd, err := unix.Openat(int(dup.Fd()), d1.Name(), flags, mode)
		if err == nil {
			f := os.NewFile(uintptr(fd), filepath.Join(dup.Name(), d1.Name()))
			err = walkFD(f, d1, flags, mode, err, fn)
		} else {
			err = walkFD(nil, d1, flags, mode, err, fn)
		}
		if err != nil {
			if err == fs.SkipDir {
				break
			}
			return err
		}
	}
	return nil
}

// WalkFD uses `openat(2)` to reimplement `filepath.WalkDir`.
// This is so that `root` or any of its parents can be moved at any point
// without disturbing the traversal, such as what happens when a Kubernetes
// mount is updated.  The sequence of operations in that case is:
//
//     CREATE ..2021_11_29_14_58_13.225465548
//     CHMOD  ..2021_11_29_14_58_13.225465548
//     RENAME ..data_tmp
//     CREATE ..data
//     REMOVE ..2021_11_29_14_56_57.996917784
//
// i.e. the root is atomically moved via `rename(2)` but the contents of the old
// directory are not changed.  This function, contrary to the one in the stdlib,
// uses POSIX `openat(2)` to maintain file descriptor references to the
// directory stack being traversed so that files are always opened relative to
// their parent, without doing a full path traversal.  Otherwise, the semantics
// are largely equivalent to the original, except for:
//
// - `fn` is given the already-opened files, which are effectively opened as
//   if `OpenFile(…, flags, mode)` had been called.
// - `fn` owns all file descriptors it receives via its first argument (so it
//   can use them asynchronously independently of `WalkFD`).  Directories are
//   `dup(2)`ed internally as needed.
// - Iteration cannot proceed if a directory cannot be `Open`ed, as there is no
//   way then to perform the `ReadDir`.  In this case, `fn` is called as usual
//   and can replace the error, but even returning `nil` will end the iteration
//   (and cause `WalkFD` to return no error).
// - The `Name()` of each file is the full path starting from `root.Name()`.
//   The `d` parameter contains the relative path as provided by `ReadDir`.
func WalkFD(root *os.File, flags int, mode os.FileMode, fn WalkFDFn) error {
	s, err := root.Stat()
	if err != nil {
		err = fn(root, nil, err)
	} else {
		err = walkFD(root, &statDirEntry{s}, flags, uint32(mode.Perm()), nil, fn)
	}
	if err == fs.SkipDir {
		return nil
	}
	return err
}
