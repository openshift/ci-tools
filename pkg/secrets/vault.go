package secrets

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api/vault"
	"github.com/openshift/ci-tools/pkg/vaultclient"
)

type VaultClient interface {
	GetKV(path string) (*vaultclient.KVData, error)
	ListKVRecursively(path string) ([]string, error)
	UpsertKV(path string, data map[string]string) error
}

type dryRunClient struct {
	file *os.File
}

func (d dryRunClient) SetFieldOnItem(itemName, fieldName string, fieldValue []byte) error {
	_, err := fmt.Fprintf(d.file, "ItemName: %s\n\tField: \n\t\t %s: %s\n", itemName, fieldName, string(fieldValue))
	return err
}

func (d dryRunClient) SetAttachmentOnItem(itemName, attachmentName string, fileContents []byte) error {
	_, err := fmt.Fprintf(d.file, "ItemName: %s\n\tAttachment: \n\t\t %s: %s\n", itemName, attachmentName, string(fileContents))
	return err
}

func (d dryRunClient) SetPassword(itemName string, password []byte) error {
	_, err := fmt.Fprintf(d.file, "ItemName: %s\n\tAttribute: \n\t\t Password: %s\n", itemName, string(password))
	return err
}

func (d dryRunClient) UpdateNotesOnItem(itemName, notes string) error {
	_, err := fmt.Fprintf(d.file, "ItemName: %s\n\tNotes: %s\n", itemName, notes)
	return err
}

func (d dryRunClient) GetAttachmentOnItem(_, _ string) ([]byte, error) {
	return nil, nil
}
func (d dryRunClient) GetFieldOnItem(_, _ string) ([]byte, error) {
	return nil, nil
}

func (d dryRunClient) GetInUseInformationForAllItems(_ string) (map[string]SecretUsageComparer, error) {
	return nil, nil
}
func (d dryRunClient) GetPassword(_ string) ([]byte, error) {
	return nil, nil
}
func (d dryRunClient) GetUserSecrets() (map[types.NamespacedName]map[string]string, error) {
	return nil, nil
}

func (d dryRunClient) HasItem(itemname string) (bool, error) {
	return false, nil
}

func (d dryRunClient) Logout() ([]byte, error) {
	return nil, nil
}

func NewDryRunClient(outputFile *os.File) Client {
	return dryRunClient{
		file: outputFile,
	}
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

func (c *vaultClient) setItemAtPath(path, field string, content string) error {
	path = c.pathFor(path)
	var data map[string]string
	if current, err := c.upstream.GetKV(path); err != nil {
		if !vaultclient.IsNotFound(err) {
			return err
		}
		data = map[string]string{field: content}
	} else {
		data = current.Data
		data[field] = content
	}
	c.censor.AddSecrets(content)
	return c.upstream.UpsertKV(path, data)
}

func (c *vaultClient) HasItem(itemName string) (bool, error) {
	path := c.pathFor(itemName)
	_, err := c.upstream.GetKV(path)
	if err != nil {
		if vaultclient.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
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

func (c *vaultClient) GetInUseInformationForAllItems(optionalSubPath string) (map[string]SecretUsageComparer, error) {
	prefix := c.prefix
	if optionalSubPath != "" {
		prefix = prefix + "/" + optionalSubPath
	}
	allKeys, err := c.upstream.ListKVRecursively(prefix)
	if err != nil {
		return nil, err
	}
	result := make(map[string]SecretUsageComparer, len(allKeys))
	var errs []error
	var lock sync.Mutex
	var wg sync.WaitGroup

	for _, key := range allKeys {
		wg.Add(1)
		key := key
		go func() {
			defer wg.Done()
			kvData, err := c.upstream.GetKV(key)
			lock.Lock()
			defer lock.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			comparer := vaultSecretUsageComparer{item: *kvData, allFields: sets.String{}, inUseFields: sets.String{}}
			for key := range kvData.Data {
				comparer.allFields.Insert(key)
			}
			result[strings.TrimPrefix(key, c.prefix+"/")] = &comparer
		}()
	}

	wg.Wait()
	return result, nil
}

func (c *vaultClient) SetFieldOnItem(itemName, fieldName string, fieldValue []byte) error {
	return c.setItemAtPath(itemName, fieldName, string(fieldValue))
}

func (c *vaultClient) SetAttachmentOnItem(itemName, attachmentName string, fileContents []byte) error {
	return c.setItemAtPath(itemName, attachmentName, string(fileContents))
}

func (c *vaultClient) SetPassword(itemName string, password []byte) error {
	return c.setItemAtPath(itemName, "password", string(password))
}

func (c *vaultClient) UpdateNotesOnItem(itemName string, notes string) error {
	return c.setItemAtPath(itemName, "notes", notes)
}

func (c *vaultClient) Logout() ([]byte, error) { return nil, nil }

func (c *vaultClient) GetUserSecrets() (map[types.NamespacedName]map[string]string, error) {
	allItems, err := c.upstream.ListKVRecursively(c.prefix)
	if err != nil {
		return nil, err
	}

	result := map[types.NamespacedName]map[string]string{}
	var errs []error
	var lock sync.Mutex
	var wg sync.WaitGroup

	for _, path := range allItems {
		path := path
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, err := c.upstream.GetKV(path)
			lock.Lock()
			defer lock.Unlock()

			if err != nil {
				errs = append(errs, err)
				return
			}
			if item.Data[vault.SecretSyncTargetNamepaceKey] == "" || item.Data[vault.SecretSyncTargetNameKey] == "" {
				return
			}
			nn := types.NamespacedName{Namespace: item.Data[vault.SecretSyncTargetNamepaceKey], Name: item.Data[vault.SecretSyncTargetNameKey]}
			if val, ok := result[nn]; ok {
				errs = append(errs, fmt.Errorf("both the %s and the %s vault item point to the %s secret", val[vault.VaultSourceKey], path, nn.String()))
				return
			}
			result[nn] = map[string]string{vault.VaultSourceKey: path}
			for k, v := range item.Data {
				if k == vault.SecretSyncTargetNamepaceKey || k == vault.SecretSyncTargetNameKey {
					continue
				}
				result[nn][k] = v
			}
		}()
	}
	wg.Wait()

	return result, utilerrors.NewAggregate(errs)
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
