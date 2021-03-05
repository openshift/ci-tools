package vaultclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/vault/api"
)

func New(addr, token string) (*VaultClient, error) {
	client, err := api.NewClient(&api.Config{Address: addr})
	if err != nil {
		return nil, err
	}
	client.SetToken(token)
	return &VaultClient{client}, nil
}

type VaultClient struct {
	*api.Client
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
			if strings.HasSuffix(child, "/") {
				// staticcheck complains `this result of append is never used, except maybe in other appends` but
				// we do use it in the initial range.
				// nolint: staticcheck
				paths = append(paths, child)
			} else {
				result = append(result, strings.Join([]string{path, child}, "/"))
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
	return &response, v.readInto(InsertDataIntoPath(path), &response)
}

func (v *VaultClient) UpsertKV(path string, data map[string]string) error {
	_, err := v.Logical().Write(InsertDataIntoPath(path), map[string]interface{}{"data": data})
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
