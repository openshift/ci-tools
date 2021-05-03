package vaultclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/vault/api"
	"github.com/sirupsen/logrus"
)

func getKuberntesAuthToken(client *VaultClient, role string) (string, time.Duration, error) {

	// Clone the client before resetting the token
	var err error
	client.Client, err = client.Client.Clone()
	if err != nil {
		return "", 0, fmt.Errorf("failed to clone client: %w", err)
	}
	client.SetToken("")

	serviceAccountToken, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return "", 0, fmt.Errorf("failed to read serviceAccountToken from /var/run/secrets/kubernetes.io/serviceaccount/token: %w", err)
	}
	resp, err := client.Logical().Write("auth/kubernetes/login", map[string]interface{}{
		"role": role,
		"jwt":  string(serviceAccountToken),
	})
	if err != nil {
		return "", 0, fmt.Errorf("failed to log into vault: %w", err)
	}

	ttl, err := resp.TokenTTL()
	if err != nil {
		return "", 0, fmt.Errorf("failed to get ttl from token: %w", err)
	}

	return resp.Auth.ClientToken, ttl, nil
}

func NewFromKubernetesAuth(addr, role string) (*VaultClient, error) {
	upstreamClient, err := api.NewClient(&api.Config{Address: addr})
	if err != nil {
		return nil, fmt.Errorf("failed to construct client: %w", err)
	}
	client := &VaultClient{Client: upstreamClient}
	token, ttl, err := getKuberntesAuthToken(client, role)
	if err != nil {
		return nil, err
	}
	client.SetToken(token)
	go client.refreshTokenWhenNeeded(ttl, func(client *VaultClient) (string, time.Duration, error) {
		return getKuberntesAuthToken(client, role)
	})

	return client, nil
}

func NewFromUserPass(addr, user, pass string) (*VaultClient, error) {
	client, err := api.NewClient(&api.Config{Address: addr})
	if err != nil {
		return nil, err
	}
	resp, err := client.Logical().Write(fmt.Sprintf("auth/userpass/login/%s", user), map[string]interface{}{"password": pass})
	if err != nil {
		return nil, fmt.Errorf("failed to login: %w", err)
	}
	client.SetToken(resp.Auth.ClientToken)
	return &VaultClient{Client: client}, nil
}

func New(addr, token string) (*VaultClient, error) {
	client, err := api.NewClient(&api.Config{Address: addr})
	if err != nil {
		return nil, err
	}
	client.SetToken(token)
	return &VaultClient{Client: client}, nil
}

type VaultClient struct {
	*api.Client
	isCredentialExpired     bool
	isCredentialExpiredLock sync.Mutex
}

func (v *VaultClient) IsCredentialExpired() bool {
	v.isCredentialExpiredLock.Lock()
	defer v.isCredentialExpiredLock.Unlock()
	return v.isCredentialExpired
}

func (v *VaultClient) refreshTokenWhenNeeded(ttl time.Duration, refreshFn func(*VaultClient) (string, time.Duration, error)) {
	var newToken string
	var err error
	for {
		time.Sleep(ttl / 2)

		expiry := time.Now().Add(ttl / 2)
		try := 1
		for {
			if time.Now().After(expiry) {
				v.isCredentialExpiredLock.Lock()
				v.isCredentialExpired = true
				v.isCredentialExpiredLock.Unlock()
			}

			newToken, ttl, err = refreshFn(v)
			if err != nil {
				logrus.WithError(err).WithField("try", try).Error("failed to refresh vault token")
				try++
				time.Sleep(2 * time.Second)
				continue
			}

			v.SetToken(newToken)
			v.isCredentialExpiredLock.Lock()
			v.isCredentialExpired = false
			v.isCredentialExpiredLock.Unlock()
			break
		}
	}
}

func (v *VaultClient) GetUserFromAliasName(userName string) (*Entity, error) {
	rawAliases, err := v.Client.Logical().List("identity/entity-alias/id")
	if err != nil {
		return nil, fmt.Errorf("failed to list aliases: %w", err)
	}

	var aliases aliasListData
	if err := dataInto(rawAliases.Data, &aliases); err != nil {
		return nil, err
	}

	var userID string
	for _, alias := range aliases.KeyInfo {
		if alias.Name == userName {
			userID = alias.CanonicalID
			break
		}
	}

	if userID == "" {
		return nil, &api.ResponseError{StatusCode: http.StatusNotFound, Errors: []string{fmt.Sprintf("no user alias named %s found", userName)}}
	}

	return v.GetUserByID(userID)
}

func (v *VaultClient) ListKV(path string) ([]string, error) {
	var keyResponse keyResponse
	if err := v.listInto(InsertMetadataIntoPath(path), &keyResponse); err != nil {
		return nil, err
	}
	return keyResponse.Keys, nil
}

func (v *VaultClient) ListKVRecursively(path string) ([]string, error) {
	paths := []string{path}
	var result []string
	for _, path := range paths {
		children, err := v.ListKV(path)
		if err != nil {
			return nil, fmt.Errorf("failed to list %s: %w", path, err)
		}
		for _, child := range children {
			// strings.Join doesn't deal with the case of "element ends with separator"
			if !strings.HasSuffix(path, "/") {
				child = "/" + child
			}
			child = path + child
			if strings.HasSuffix(child, "/") {
				grandchildren, err := v.ListKVRecursively(child)
				if err != nil {
					return nil, err
				}
				result = append(result, grandchildren...)
			} else {
				result = append(result, child)
			}
		}
	}

	return result, nil
}

func (v *VaultClient) DestroyKVIrreversibly(path string) error {
	_, err := v.Logical().Delete(InsertMetadataIntoPath(path))
	return err
}

func (v *VaultClient) GetKV(path string) (*KVData, error) {
	var response KVData
	if err := v.readInto(InsertDataIntoPath(path), &response); err != nil {
		return nil, fmt.Errorf("failed to get item at path %q: %w", path, err)
	}
	return &response, nil
}

func (v *VaultClient) UpsertKV(path string, data map[string]string) error {
	// Get it first to avoid creating a new revision when the content didn't change
	currentData, err := v.GetKV(path)
	if err != nil {
		if !IsNotFound(err) {
			return err
		}
	}
	if currentData != nil && reflect.DeepEqual(currentData.Data, data) {
		return nil
	}
	_, err = v.Logical().Write(InsertDataIntoPath(path), map[string]interface{}{"data": data})
	return err
}

// InsertMetadataIntoPath inserts '/metadata' as second element into a given
// path (which itself might have only one element(
func InsertMetadataIntoPath(path string) string {
	i := strings.Index(path, "/")
	if i < 0 {
		return path + "/metadata"
	}
	return path[:i] + "/metadata" + path[i:]
}

// InsertDataIntoPath inserts '/data' as second element into a given
// path (which itself might have only one element(
func InsertDataIntoPath(path string) string {
	i := strings.Index(path, "/")
	if i < 0 {
		return path + "/data"
	}
	return path[:i] + "/data" + path[i:]
}

func (v *VaultClient) Put(path string, body []byte) error {
	r := v.Client.NewRequest("PUT", "/v1/"+path)
	r.BodyBytes = body

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	resp, err := v.RawRequestWithContext(ctx, r)
	if resp != nil {
		defer resp.Body.Close()
	}

	return err
}

func (v *VaultClient) GetUserByID(id string) (*Entity, error) {
	var entity Entity
	return &entity, v.readInto(fmt.Sprintf("identity/entity/id/%s", id), &entity)
}

func (v *VaultClient) GetGroupNames() ([]string, error) {
	var result keyResponse
	if err := v.listInto("identity/group/name", &result); err != nil {
		return nil, err
	}
	return result.Keys, nil
}

func (v *VaultClient) GetGroupByName(groupName string) (*Group, error) {
	var group Group
	return &group, v.readInto(fmt.Sprintf("identity/group/name/%s", groupName), &group)
}

func (v *VaultClient) GetAllGroups() ([]Group, error) {
	// The list endpoints return only the id/name
	names, err := v.GetGroupNames()
	if err != nil {
		return nil, err
	}

	var result []Group
	for _, name := range names {
		group, err := v.GetGroupByName(name)
		if err != nil {
			return nil, err
		}
		result = append(result, *group)
	}

	return result, nil
}

func (v *VaultClient) ListIdentities() ([]string, error) {
	var response keyResponse
	return response.Keys, v.listInto("identity/entity/id", &response)
}

func (v *VaultClient) GetGroupByID(groupID string) (*Group, error) {
	var group Group
	return &group, v.readInto(fmt.Sprintf("identity/group/id/%s", groupID), &group)
}

func (v *VaultClient) UpdateGroupMembers(groupName string, newMemberIDs []string) error {
	data := map[string]interface{}{"member_entity_ids": newMemberIDs}
	_, err := v.Logical().Write(fmt.Sprintf("identity/group/name/%s", groupName), data)
	return err
}

func (v *VaultClient) DeleteGroupByName(name string) error {
	_, err := v.Logical().Delete(fmt.Sprintf("identity/group/name/%s", name))
	return err
}

func (v *VaultClient) ListAuthMounts() (MountListResponse, error) {
	var response MountListResponse
	return response, v.readInto("sys/auth", &response)
}

func (v *VaultClient) CreateIdentity(name string, policies []string) (*Entity, error) {
	var entity *Entity
	return entity, v.writeInto("identity/entity", map[string]interface{}{"name": name, "policies": policies}, &entity)
}

func (v *VaultClient) listInto(path string, target interface{}) error {
	raw, err := v.Logical().List(path)
	if err != nil {
		return err
	}
	// 404 for list means no results: https://github.com/hashicorp/vault/issues/5861
	if raw == nil || raw.Data == nil {
		return nil
	}
	return dataInto(raw.Data, target)
}

func (v *VaultClient) readInto(path string, target interface{}) error {
	raw, err := v.Logical().Read(path)
	if err != nil {
		return err
	}
	// Some genius decided `return nil, nil` is a great way to handle 404s
	if raw == nil || raw.Data == nil {
		return &api.ResponseError{StatusCode: http.StatusNotFound}
	}
	return dataInto(raw.Data, target)
}

func (v *VaultClient) writeInto(path string, requestData map[string]interface{}, target interface{}) error {
	raw, err := v.Logical().Write(path, requestData)
	if err != nil {
		return err
	}
	// Some genius decided `return nil, nil` is a great way to handle 404s
	if raw == nil || raw.Data == nil {
		return &api.ResponseError{StatusCode: http.StatusNotFound}
	}
	return dataInto(raw.Data, target)
}

func dataInto(d map[string]interface{}, target interface{}) error {
	serialized, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("failed to serialize data from response: %w", err)
	}
	if err := json.Unmarshal(serialized, target); err != nil {
		return fmt.Errorf("failed to unmarshal data '%s' into %T: %w", string(serialized), target, err)
	}

	return nil
}
