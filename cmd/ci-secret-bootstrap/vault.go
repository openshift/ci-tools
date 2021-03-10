package main

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/vaultclient"
)

var _ secretStoreClient = &vaultSecretStoreWrapper{}

type vaultClient interface {
	GetKV(path string) (*vaultclient.KVData, error)
	ListKVRecursively(path string) ([]string, error)
}

type vaultSecretStoreWrapper struct {
	upstream vaultClient
	prefix   string
}

func (w *vaultSecretStoreWrapper) pathFor(item string) string {
	return w.prefix + "/" + item
}

func (w *vaultSecretStoreWrapper) getKeyAtPath(path, key string) ([]byte, error) {
	path = w.pathFor(path)
	response, err := w.upstream.GetKV(path)
	if err != nil {
		return nil, err
	}
	val, ok := response.Data[key]
	if !ok {
		return nil, fmt.Errorf("item at path %q has no key %q", path, key)
	}

	return []byte(val), nil
}

func (w *vaultSecretStoreWrapper) GetFieldOnItem(itemName, fieldName string) ([]byte, error) {
	return w.getKeyAtPath(itemName, fieldName)
}

func (w *vaultSecretStoreWrapper) GetAttachmentOnItem(itemName, attachmentName string) ([]byte, error) {
	return w.getKeyAtPath(itemName, attachmentName)
}

func (w *vaultSecretStoreWrapper) GetPassword(itemName string) ([]byte, error) {
	return w.getKeyAtPath(itemName, "password")
}

func (w *vaultSecretStoreWrapper) GetInUseInformationForAllItems() (map[string]secretUsageComparer, error) {
	allKeys, err := w.upstream.ListKVRecursively(w.prefix)
	if err != nil {
		return nil, err
	}
	result := make(map[string]secretUsageComparer, len(allKeys))
	for _, key := range allKeys {
		kvData, err := w.upstream.GetKV(key)
		if err != nil {
			return nil, err
		}
		comparer := vaultSecretUsageComparer{item: *kvData, allFields: sets.String{}, inUseFields: sets.String{}}
		for key := range kvData.Data {
			comparer.allFields.Insert(key)
		}
		result[strings.TrimPrefix(key, w.prefix)] = &comparer
	}

	return result, nil
}

type vaultSecretUsageComparer struct {
	item        vaultclient.KVData
	allFields   sets.String
	inUseFields sets.String
}

func (v *vaultSecretUsageComparer) LastChanged() time.Time {
	return v.item.Metadata.CreatedTime
}

func (v *vaultSecretUsageComparer) markInUse(fields sets.String) (absent sets.String) {
	v.inUseFields.Insert(fields.List()...)
	return fields.Difference(v.allFields)
}

func (v *vaultSecretUsageComparer) UnusedFields(inUse sets.String) (Difference sets.String) {
	return v.markInUse(inUse)
}

func (v *vaultSecretUsageComparer) UnusedAttachments(inUse sets.String) (Difference sets.String) {
	return v.markInUse(inUse)
}

func (v *vaultSecretUsageComparer) HasPassword() bool {
	if v.allFields.Has("password") {
		v.inUseFields.Insert("password")
		return true
	}
	return false
}

func (v *vaultSecretUsageComparer) SuperfluousFields() sets.String {
	return v.allFields.Difference(v.inUseFields)
}
