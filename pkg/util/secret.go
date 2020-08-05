package util

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// SecretFromDir creates a secret with the contents of files in a directory.
func SecretFromDir(path string) (*coreapi.Secret, error) {
	ret := &coreapi.Secret{
		Type: coreapi.SecretTypeOpaque,
		Data: make(map[string][]byte),
	}
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("could not read dir %s: %w", path, err)
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		path := filepath.Join(path, f.Name())
		// if the file is a broken symlink or a symlink to a dir, skip it
		if fi, err := os.Stat(path); err != nil || fi.IsDir() {
			continue
		}
		ret.Data[f.Name()], err = ioutil.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("could not read file %s: %w", path, err)
		}
	}
	return ret, nil
}

// UpdateSecret adds new values to an existing secret.
// New values are added, existing values are overwritten. The secret will be
// created if it doesn't already exist.
func UpdateSecret(ctx context.Context, client ctrlruntimeclient.Client, secret *coreapi.Secret) (created bool, err error) {
	err = client.Create(ctx, secret)
	if err == nil {
		return true, nil
	}
	if !kerrors.IsAlreadyExists(err) {
		return false, err
	}
	existing := &coreapi.Secret{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: secret.Name}, existing); err != nil {
		return false, err
	}
	if l := len(secret.Data); l != 0 && existing.Data == nil {
		existing.Data = make(map[string][]byte, l)
	}
	for k, v := range secret.Data {
		existing.Data[k] = v
	}
	return false, client.Update(ctx, existing)
}
