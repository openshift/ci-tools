package util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	utilpointer "k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// SecretFromDir creates a secret with the contents of files in a directory.
func SecretFromDir(path string) (*coreapi.Secret, error) {
	ret := &coreapi.Secret{
		Type: coreapi.SecretTypeOpaque,
		Data: make(map[string][]byte),
	}
	files, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("could not read dir %s: %w", path, err)
	}
	for _, f := range files {
		if f.IsDir() {
			logrus.Warningf("skipped directory %q when creating secret from directory %q", f.Name(), path)
			continue
		}
		path := filepath.Join(path, f.Name())
		// if the file is a broken symlink or a symlink to a dir, skip it
		if fi, err := os.Stat(path); err != nil || fi.IsDir() {
			continue
		}
		ret.Data[f.Name()], err = os.ReadFile(filepath.Clean(path))
		if err != nil {
			return nil, fmt.Errorf("could not read file %s: %w", path, err)
		}
	}
	return ret, nil
}

// UpsertImmutableSecret adds new values to an existing secret.
// New values are added, existing values are overwritten. The secret will be
// created if it doesn't already exist. Updating an existing secret happens by re-creating it.
func UpsertImmutableSecret(ctx context.Context, client ctrlruntimeclient.Client, secret *coreapi.Secret) (created bool, err error) {
	secret.Immutable = utilpointer.Bool(true)
	err = client.Create(ctx, secret.DeepCopy())
	if err == nil {
		return true, nil
	}
	if !kerrors.IsAlreadyExists(err) {
		return false, err
	}
	existing := &coreapi.Secret{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: secret.Name, Namespace: secret.Namespace}, existing); err != nil {
		return false, err
	}
	if equality.Semantic.DeepEqual(secret.Data, existing.Data) {
		return false, nil
	}
	if err := client.Delete(ctx, existing); err != nil {
		return false, fmt.Errorf("delete failed: %w", err)
	}

	// Recreate counts as "Update"
	return false, client.Create(ctx, secret)
}
