package util

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilpointer "k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
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

// UpsertImmutableSecret adds new values to an existing secret.
// New values are added, existing values are overwritten. The secret will be
// created if it doesn't already exist. Updating an existing secret happens by re-creating it.
func UpsertImmutableSecret(ctx context.Context, client ctrlruntimeclient.Client, secret *coreapi.Secret) (created bool, err error) {
	secret.Immutable = utilpointer.BoolPtr(true)
	err = client.Create(ctx, secret.DeepCopy())
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
	if equality.Semantic.DeepEqual(secret.Data, existing.Data) {
		return false, nil
	}
	if err := client.Delete(ctx, existing); err != nil {
		return false, fmt.Errorf("delete failed: %w", err)
	}

	// Recreate counts as "Update"
	return false, client.Create(ctx, secret)
}

// CopySecretsIntoJobNamespace copies the source secrets to the namespace where the job runs
func CopySecretsIntoJobNamespace(ctx context.Context, client ctrlruntimeclient.Client, jobSpec *api.JobSpec, secrets map[string]ctrlruntimeclient.ObjectKey) error {
	for name, secretKey := range secrets {
		src := &coreapi.Secret{}
		if err := client.Get(ctx, secretKey, src); err != nil {
			return fmt.Errorf("could not read source secret %s in namespace %s: %w", secretKey.Name, secretKey.Namespace, err)
		}
		dst := &coreapi.Secret{
			TypeMeta: src.TypeMeta,
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: jobSpec.Namespace(),
			},
			Type:       src.Type,
			Data:       src.Data,
			StringData: src.StringData,
		}
		if err := client.Create(ctx, dst); err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create destination secert %s in namespace %s: %w", name, jobSpec.Namespace(), err)
		}
	}
	return nil
}
