package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/vault/api"

	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/pkg/vaultclient"
)

func TestSecretCollectionManager(t *testing.T) {
	t.Parallel()
	vaultAddr := testhelper.Vault(t)

	client, err := vaultclient.New("http://"+vaultAddr, testhelper.VaultTestingRootToken)
	if err != nil {
		t.Fatalf("failed to construct vault client: %v", err)
	}

	if err := client.Sys().EnableAuthWithOptions("userpass", &api.EnableAuthOptions{Type: "userpass"}); err != nil {
		t.Fatalf("failed to enable userpass auth: %v", err)
	}

	mounts, err := client.ListAuthMounts()
	if err != nil {
		t.Fatalf("failed to list auth mounts: %v", err)
	}
	var mountAccessor string
	for _, mount := range mounts {
		if mount.Type == "userpass" {
			mountAccessor = mount.Accessor
			break
		}
	}
	if mountAccessor == "" {
		t.Fatalf("failed to find userpass mount")
	}

	for _, user := range []string{"user-1", "user-2"} {
		if _, err := client.Logical().Write(fmt.Sprintf("/auth/userpass/users/%s", user), map[string]interface{}{"password": "password"}); err != nil {
			t.Fatalf("failed to create userpass user %s: %v", user, err)
		}
		identity, err := client.CreateIdentity(user, []string{"default"})
		if err != nil {
			t.Fatalf("failed to create identity for user %s: %v", user, err)
		}
		if _, err := client.Logical().Write("identity/entity-alias", map[string]interface{}{
			"name":           user,
			"canonical_id":   identity.ID,
			"mount_accessor": mountAccessor,
		}); err != nil {
			t.Fatalf("failed to create identity alias for user %s in mount_accessor %s: %v", user, mountAccessor, err)
		}
	}

	managerListenAddr := "127.0.0.1:" + testhelper.GetFreePort(t)
	server := server(client, "secret/self-managed", managerListenAddr)
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			t.Errorf("failed to start secret-collection-manager: %v", err)
		}
	}()

	testhelper.WaitForHTTP200(fmt.Sprintf("http://%s/healthz", managerListenAddr), "secret-collection-manager", t)
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("failed to close server: %v", err)
		}
	})

	type permCheckScenario struct {
		user          string
		path          string
		expectSuccess bool
	}

	type dataCheckScenario struct {
		user         string
		path         string
		expectedData map[string]string
	}

	testCases := []struct {
		name                  string
		user                  string
		request               *http.Request
		expectedStatusCode    int
		expectedBody          string
		expectedVaultGroups   []vaultclient.Group
		expectedVaultPolicies []string
		dataCheckScenario     []dataCheckScenario
		permCheckScenarios    []permCheckScenario
	}{
		{
			name:                  "Initial listing as user 1, no collections",
			user:                  "user-1",
			request:               mustNewRequest(http.MethodGet, fmt.Sprintf("http://%s/secretcollection", managerListenAddr)),
			expectedStatusCode:    200,
			expectedVaultPolicies: []string{"default", "root"},
			permCheckScenarios: []permCheckScenario{
				{"user-1", "secret/self-managed/mine-alone", false},
				{"user-2", "secret/self-managed/mine-alone", false},
			},
		},
		{
			name:                  "Initial listing as user 2, no collections",
			user:                  "user-2",
			request:               mustNewRequest(http.MethodGet, fmt.Sprintf("http://%s/secretcollection", managerListenAddr)),
			expectedStatusCode:    200,
			expectedVaultPolicies: []string{"default", "root"},
		},
		{
			name:                  "Listing users returns all users",
			user:                  "user-1",
			request:               mustNewRequest(http.MethodGet, fmt.Sprintf("http://%s/users", managerListenAddr)),
			expectedStatusCode:    200,
			expectedBody:          `["user-1","user-2"]`,
			expectedVaultPolicies: []string{"default", "root"},
		},
		{
			name:                  "User 1 creates a colletion with an invalid name",
			user:                  "user-1",
			request:               mustNewRequest(http.MethodPut, fmt.Sprintf("http://%s/secretcollection/name%%20withIllegalComponents", managerListenAddr)),
			expectedStatusCode:    400,
			expectedVaultPolicies: []string{"default", "root"},
			expectedBody:          "name \"name withIllegalComponents\" does not match regex '^[a-z0-9-]+$'\n",
		},
		{
			name:               "User 1 creates collection",
			user:               "user-1",
			request:            mustNewRequest(http.MethodPut, fmt.Sprintf("http://%s/secretcollection/mine-alone", managerListenAddr)),
			expectedStatusCode: 200,
			expectedVaultGroups: []vaultclient.Group{{
				Name:            "secret-collection-manager-managed-mine-alone",
				Policies:        []string{"secret-collection-manager-managed-mine-alone"},
				MemberEntityIDs: []string{"entity-0"},
				Metadata:        map[string]string{"created-by-secret-collection-manager": "true"},
				ModifyIndex:     1,
			}},
			expectedVaultPolicies: []string{"default", "secret-collection-manager-managed-mine-alone", "root"},
			dataCheckScenario:     []dataCheckScenario{{"user-1", "secret/self-managed/mine-alone/index", map[string]string{".": "."}}},
			permCheckScenarios: []permCheckScenario{
				{"user-1", "secret/self-managed/mine-alone", true},
				{"user-2", "secret/self-managed/mine-alone", false},
				{"user-1", "secret/self-managed/elsewhere", false},
				{"user-2", "secret/self-managed/elsewhere", false},
			},
		},
		{
			name:               "Listing as user-1, collection is returned",
			user:               "user-1",
			request:            mustNewRequest(http.MethodGet, fmt.Sprintf("http://%s/secretcollection", managerListenAddr)),
			expectedStatusCode: 200,
			expectedBody:       `[{"name":"mine-alone","path":"secret/self-managed/mine-alone","members":["user-1"]}]`,
			expectedVaultGroups: []vaultclient.Group{{
				Name:            "secret-collection-manager-managed-mine-alone",
				Policies:        []string{"secret-collection-manager-managed-mine-alone"},
				MemberEntityIDs: []string{"entity-0"},
				Metadata:        map[string]string{"created-by-secret-collection-manager": "true"},
				ModifyIndex:     1,
			}},
			expectedVaultPolicies: []string{"default", "secret-collection-manager-managed-mine-alone", "root"},
		},
		{
			name:               "Listing as user 2, no collections",
			user:               "user-2",
			request:            mustNewRequest(http.MethodGet, fmt.Sprintf("http://%s/secretcollection", managerListenAddr)),
			expectedStatusCode: 200,
			expectedVaultGroups: []vaultclient.Group{{
				Name:            "secret-collection-manager-managed-mine-alone",
				Policies:        []string{"secret-collection-manager-managed-mine-alone"},
				MemberEntityIDs: []string{"entity-0"},
				Metadata:        map[string]string{"created-by-secret-collection-manager": "true"},
				ModifyIndex:     1,
			}},
			expectedVaultPolicies: []string{"default", "secret-collection-manager-managed-mine-alone", "root"},
		},
		{
			name:               "User is not a collection member, 404",
			user:               "user-2",
			request:            mustNewRequest(http.MethodDelete, fmt.Sprintf("http://%s/secretcollection/mine-alone", managerListenAddr)),
			expectedStatusCode: 404,
			expectedVaultGroups: []vaultclient.Group{{
				Name:            "secret-collection-manager-managed-mine-alone",
				Policies:        []string{"secret-collection-manager-managed-mine-alone"},
				MemberEntityIDs: []string{"entity-0"},
				Metadata:        map[string]string{"created-by-secret-collection-manager": "true"},
				ModifyIndex:     1,
			}},
			expectedVaultPolicies: []string{"default", "secret-collection-manager-managed-mine-alone", "root"},
		},
		{
			name:               "Request to remove all members from a collection, 400",
			user:               "user-1",
			request:            mustNewRequest(http.MethodPut, fmt.Sprintf("http://%s/secretcollection/mine-alone/members", managerListenAddr), []byte(`{}`)...),
			expectedStatusCode: 400,
			expectedVaultGroups: []vaultclient.Group{{
				Name:            "secret-collection-manager-managed-mine-alone",
				Policies:        []string{"secret-collection-manager-managed-mine-alone"},
				MemberEntityIDs: []string{"entity-0"},
				Metadata:        map[string]string{"created-by-secret-collection-manager": "true"},
				ModifyIndex:     1,
			}},
			expectedVaultPolicies: []string{"default", "secret-collection-manager-managed-mine-alone", "root"},
		},
		{
			name: "Add a new collection member",
			user: "user-1",
			request: mustNewRequest(http.MethodPut, fmt.Sprintf("http://%s/secretcollection/mine-alone/members", managerListenAddr),
				[]byte(`{"members":["user-1","user-2"]}`)...,
			),
			expectedStatusCode: 200,
			expectedVaultGroups: []vaultclient.Group{{
				Name:            "secret-collection-manager-managed-mine-alone",
				Policies:        []string{"secret-collection-manager-managed-mine-alone"},
				MemberEntityIDs: []string{"entity-0", "entity-1"},
				Metadata:        map[string]string{"created-by-secret-collection-manager": "true"},
				ModifyIndex:     2,
			}},
			expectedVaultPolicies: []string{"default", "secret-collection-manager-managed-mine-alone", "root"},
		},
		{
			name:                  "New collection member successfully deletes it",
			user:                  "user-2",
			request:               mustNewRequest(http.MethodDelete, fmt.Sprintf("http://%s/secretcollection/mine-alone", managerListenAddr)),
			expectedStatusCode:    200,
			expectedVaultPolicies: []string{"default", "secret-collection-manager-managed-mine-alone", "root"},
		},
	}

	// These tests mutate state in vault, so they need to be executed serially
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.request.Header.Set("X-Forwarded-Email", fmt.Sprintf("%s@unchecked.com", tc.user))
			response, err := http.DefaultClient.Do(tc.request)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.expectedStatusCode {
				t.Fatalf("expected status code %d, got %d", tc.expectedStatusCode, response.StatusCode)
			}

			bodyData, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("failed to read response body: %v", err)
			}

			// Do not check response body on errors, it contains an unpredictable UUID and we do not care a lot
			// about stability of error bodies anyways, as we mostly communicate through the status.
			if response.StatusCode < 300 {
				if diff := cmp.Diff(tc.expectedBody, string(bodyData)); diff != "" {
					t.Errorf("expected body differs from actual: %s", diff)
				}
			}

			groups, err := client.GetAllGroups()
			if err != nil {
				t.Fatalf("failed to get all groups: %v", err)
			}
			for idx := range groups {
				groups[idx].ID = ""
				groups[idx].CreationTime = nil
				groups[idx].LastUpdateTime = nil
				groups[idx].Type = ""
				groups[idx].NamespaceID = ""
				if reflect.DeepEqual(groups[idx].Alias, emptyVaultAlias) {
					// The server doesn't use omitempty, so defining it as pointer with omitempty clientside is useless,
					groups[idx].Alias = nil
				}
				for memberIdIdx := range groups[idx].MemberEntityIDs {
					groups[idx].MemberEntityIDs[memberIdIdx] = fmt.Sprintf("entity-%d", memberIdIdx)
				}
			}
			if diff := cmp.Diff(tc.expectedVaultGroups, groups); diff != "" {
				t.Errorf("expectedVaultGroups differ from actual: %s", diff)
			}
			policies, err := client.Sys().ListPolicies()
			if err != nil {
				t.Fatalf("failed to list policies: %v", err)
			}
			if diff := cmp.Diff(tc.expectedVaultPolicies, policies); diff != "" {
				t.Errorf("expected vault policies differ from actual: %s", diff)
			}

			for _, scenario := range tc.dataCheckScenario {
				scenario := scenario
				t.Run(fmt.Sprintf("path: %s, user: %s, data: %v", scenario.path, scenario.user, scenario.expectedData), func(t *testing.T) {
					t.Parallel()
					client, err := vaultclient.NewFromUserPass("http://"+vaultAddr, scenario.user, "password")
					if err != nil {
						t.Fatalf("failed to construct vault client: %v", err)
					}
					result, err := client.GetKV(scenario.path)
					if err != nil {
						t.Fatalf("failed to get %s: %v", scenario.path, err)
					}
					if diff := cmp.Diff(result.Data, scenario.expectedData); diff != "" {
						t.Errorf("actual data differs from expected: %s", diff)
					}
				})
			}

			for _, scenario := range tc.permCheckScenarios {
				permCheckScenario := scenario
				t.Run(fmt.Sprintf("path: %s, user: %s, expectSuccess: %t", permCheckScenario.path, permCheckScenario.user, permCheckScenario.expectSuccess), func(t *testing.T) {
					t.Parallel()
					client, err := vaultclient.NewFromUserPass("http://"+vaultAddr, scenario.user, "password")
					if err != nil {
						t.Fatalf("failed to construct vault client: %v", err)
					}
					initialResult, err := client.ListKV(scenario.path)
					checkIs403(err, "initial list", scenario.expectSuccess, t)
					if err == nil && len(initialResult) != 0 {
						t.Errorf("initial list returned more than zero results: %v", initialResult)
					}
					data := map[string]string{"foo": "bar"}
					checkIs403(client.UpsertKV(scenario.path+"/my-secret", data), "upsert secret", scenario.expectSuccess, t)

					retrieved, err := client.GetKV(scenario.path + "/my-secret")
					checkIs403(err, "retrieve secret", scenario.expectSuccess, t)
					if err == nil {
						if diff := cmp.Diff(data, retrieved.Data); diff != "" {
							t.Errorf("retrieved secret differs from created: %s", diff)
						}
					}

				})
			}
		})

	}
	t.Run("Everything was deleted", func(t *testing.T) {
		results, err := client.ListKVRecursively("secret/self-managed/mine-alone")
		if err != nil {
			t.Fatalf("failed to list recuresively: %v", err)
		}
		if len(results) > 0 {
			t.Errorf("expected kv store to be empty, but found %v", results)
		}
	})

}

func checkIs403(err error, action string, expectSuccess bool, t *testing.T) {
	if expectSuccess {
		if err != nil {
			t.Errorf("%s failed: %v", action, err)
		}
		return
	}
	if err == nil {
		t.Errorf("action %s unexpectedyly succeeded", action)
		return
	}
	responseErr := &api.ResponseError{}
	if !errors.As(err, &responseErr) {
		t.Errorf("error was not a *responseErr, but a %T", err)
	} else if responseErr.StatusCode != 403 {
		t.Errorf("expected status code to be 403, was %d", responseErr.StatusCode)
	}
}

var emptyVaultAlias = &vaultclient.Alias{}

func mustNewRequest(method, url string, body ...byte) *http.Request {
	request, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		panic(err)
	}
	return request
}
