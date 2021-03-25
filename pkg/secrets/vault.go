package secrets

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/vaultclient"
)

type VaultClient interface {
	GetKV(path string) (*vaultclient.KVData, error)
	ListKVRecursively(path string) ([]string, error)
}

type vaultClient struct {
	upstream VaultClient
	prefix   string
	censor   *DynamicCensor
}

func NewVaultClient(upstream VaultClient, prefix string, censor *DynamicCensor) Client {
	return &vaultClient{
		upstream: upstream,
		prefix:   prefix,
		censor:   censor,
	}
}

func (c *vaultClient) pathFor(item string) string {
	return c.prefix + "/" + item
}

func (c *vaultClient) getKeyAtPath(path, key string) ([]byte, error) {
	path = c.pathFor(path)
	response, err := c.upstream.GetKV(path)
	if err != nil {
		return nil, err
	}
	val, ok := response.Data[key]
	if !ok {
		return nil, fmt.Errorf("item at path %q has no key %q", path, key)
	}

	return []byte(val), nil
}

func (c *vaultClient) getSecretAtPath(path, key string) ([]byte, error) {
	ret, err := c.getKeyAtPath(path, key)
	if err == nil {
		c.censor.AddSecrets(string(ret))
	}
	return ret, err
}

func (c *vaultClient) GetFieldOnItem(itemName, fieldName string) ([]byte, error) {
	return c.getSecretAtPath(itemName, fieldName)
}

func (c *vaultClient) GetAttachmentOnItem(itemName, attachmentName string) ([]byte, error) {
	return c.getSecretAtPath(itemName, attachmentName)
}

func (c *vaultClient) GetPassword(itemName string) ([]byte, error) {
	return c.getSecretAtPath(itemName, "password")
}

func (c *vaultClient) GetInUseInformationForAllItems() (map[string]SecretUsageComparer, error) {
	allKeys, err := c.upstream.ListKVRecursively(c.prefix)
	if err != nil {
		return nil, err
	}
	result := make(map[string]SecretUsageComparer, len(allKeys))
	for _, key := range allKeys {
		kvData, err := c.upstream.GetKV(key)
		if err != nil {
			return nil, err
		}
		comparer := vaultSecretUsageComparer{item: *kvData, allFields: sets.String{}, inUseFields: sets.String{}}
		for key := range kvData.Data {
			comparer.allFields.Insert(key)
		}
		result[strings.TrimPrefix(key, c.prefix)] = &comparer
	}

	return result, nil
}

func (c *vaultClient) Logout() ([]byte, error) { return nil, nil }

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
