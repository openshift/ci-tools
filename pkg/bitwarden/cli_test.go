package bitwarden

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestLoginAndListItems(t *testing.T) {
	revDate, err := time.Parse(time.RFC3339, "2019-10-11T23:33:21.970Z")
	if err != nil {
		t.Fatal("Failed to parse a date")
	}
	testCases := []struct {
		name               string
		username           string
		password           string
		responses          map[string]execResponse
		expectedCalls      [][]string
		expectedSession    string
		expectedSavedItems []Item
		expectedError      error
	}{
		{
			name:     "basic case",
			username: "u",
			password: "p",
			responses: map[string]execResponse{
				"login u p --response": {
					out: []byte(`{
  "success": true,
  "data": {
    "noColor": false,
    "object": "message",
    "title": "You are logged in!",
    "message": "\nTo unlock your vault, set ...",
    "raw": "not-going-to-tell-you=="
  }
}`),
				},
				"--session not-going-to-tell-you== list items": {
					out: []byte(`[
  {
    "object": "item",
    "id": "id1",
    "organizationId": "org1",
    "folderId": null,
    "type": 2,
    "name": "unsplash.com",
    "notes": null,
    "favorite": false,
    "fields": [
      {
        "name": "API Key",
        "value": "value1",
        "type": 0
      }
    ],
    "secureNote": {
      "type": 0
    },
    "collectionIds": [
      "id1"
    ],
    "revisionDate": "2019-10-11T23:33:21.970Z"
  },
  {
    "object": "item",
    "id": "id2",
    "organizationId": "org1",
    "folderId": null,
    "type": 2,
    "name": "my-credentials",
    "login": {
      "username": "xxx",
      "password": "yyy"
    },
    "notes": "important notes",
    "favorite": false,
    "secureNote": {
      "type": 0
    },
    "collectionIds": [
      "id2"
    ],
    "attachments": [
      {
        "id": "a-id1",
        "fileName": "secret.auto.vars",
        "size": "161",
        "sizeName": "161 Bytes",
        "url": "https://cdn.bitwarden.net/attachments/111/222"
      }
    ],
    "revisionDate": "2019-10-11T23:33:21.970Z"
  }
]`),
				},
			},
			expectedCalls: [][]string{
				{"login", "u", "p", "--response"},
				{"--session", "not-going-to-tell-you==", "list", "items"},
			},
			expectedSession: "not-going-to-tell-you==",
			expectedSavedItems: []Item{
				{
					ID:           "id1",
					Name:         "unsplash.com",
					Organization: "org1",
					Type:         2,
					RevisionTime: &revDate,
					Fields: []Field{
						{
							Name:  "API Key",
							Value: "value1",
						},
					},
					Collections: []string{"id1"},
				},
				{
					ID:           "id2",
					Name:         "my-credentials",
					Organization: "org1",
					Type:         2,
					Login:        &Login{Password: "yyy"},
					RevisionTime: &revDate,
					Attachments: []Attachment{
						{
							ID:       "a-id1",
							FileName: "secret.auto.vars",
						},
					},
					Notes:       "important notes",
					Collections: []string{"id2"},
				},
			},
		},
		{
			name:     "some unknown error on list cmd",
			username: "u",
			password: "p",
			responses: map[string]execResponse{
				"login u p --response": {
					out: []byte(`{
  "success": true,
  "data": {
    "noColor": false,
    "object": "message",
    "title": "You are logged in!",
    "message": "\nTo unlock your vault, set ...",
    "raw": "not-going-to-tell-you=="
  }
}`),
				},
				"--session not-going-to-tell-you== list items": {
					err: errors.New("some unknown error"),
				},
			},
			expectedCalls: [][]string{
				{"login", "u", "p", "--response"},
				{"--session", "not-going-to-tell-you==", "list", "items"},
			},
			expectedSession: "not-going-to-tell-you==",
			expectedError:   errors.New("some unknown error"),
		},
		{
			name:     "u/p not matching",
			username: "u",
			password: "p",
			responses: map[string]execResponse{
				"login u p --response": {
					out: []byte(`{
  "success": false,
  "message": "Username or password is incorrect. Try again."
}`),
					err: errors.New("Username or password is incorrect. Try again"),
				},
			},
			expectedCalls: [][]string{
				{"login", "u", "p", "--response"},
			},
			expectedError: errors.New("failed to log in: Username or password is incorrect. Try again"),
		},
		{
			name:     "already logged in",
			username: "u",
			password: "p",
			responses: map[string]execResponse{
				"login u p --response": {
					out: []byte(`{
  "success": false,
  "message": "You are already logged in as dptp@redhat.com."
}`),
					err: errors.New("You are already logged in as dptp@redhat.com"),
				},
			},
			expectedCalls: [][]string{
				{"login", "u", "p", "--response"},
			},
			expectedError: errors.New("failed to log in: You are already logged in as dptp@redhat.com"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := fakeExecutor{
				records:   [][]string{},
				responses: tc.responses,
			}
			client := cliClient{
				username:   tc.username,
				password:   tc.password,
				run:        e.Run,
				addSecrets: func(...string) {},
			}
			actualError := client.loginAndListItems()

			equalError(t, tc.expectedError, actualError)
			equal(t, tc.expectedSession, client.session)
			equal(t, tc.expectedSavedItems, client.savedItems)
			equal(t, tc.expectedCalls, e.records)
		})
	}
}

func TestGetFieldOnItem(t *testing.T) {
	testCases := []struct {
		name        string
		client      *cliClient
		itemName    string
		fieldName   string
		expected    []byte
		expectedErr error
	}{
		{
			name: "basic case",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id1",
						Name: "unsplash.com",
						Fields: []Field{
							{
								Name:  "API Key",
								Value: "value1",
							},
							{
								Name:  "no name",
								Value: "value2",
							},
						},
					},
					{
						ID:   "id2",
						Name: "my-credentials",
						Attachments: []Attachment{
							{
								ID:       "a-id1",
								FileName: "secret.auto.vars",
							},
						},
					},
				},
			},
			itemName:  "unsplash.com",
			fieldName: "API Key",
			expected:  []byte("value1"),
		},
		{
			name: "item not find",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id1",
						Name: "unsplash.com",
						Fields: []Field{
							{
								Name:  "API Key",
								Value: "value1",
							},
							{
								Name:  "no name",
								Value: "value2",
							},
						},
					},
					{
						ID:   "id2",
						Name: "my-credentials",
						Attachments: []Attachment{
							{
								ID:       "a-id1",
								FileName: "secret.auto.vars",
							},
						},
					},
				},
			},
			itemName:    "no-item",
			fieldName:   "API Key",
			expectedErr: errors.New("failed to find field API Key in item no-item"),
		},
		{
			name: "field not found",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id1",
						Name: "unsplash.com",
						Fields: []Field{
							{
								Name:  "API Key",
								Value: "value1",
							},
							{
								Name:  "no name",
								Value: "value2",
							},
						},
					},
					{
						ID:   "id2",
						Name: "my-credentials",
						Attachments: []Attachment{
							{
								ID:       "a-id1",
								FileName: "secret.auto.vars",
							},
						},
					},
				},
			},
			itemName:    "unsplash.com",
			fieldName:   "no-field",
			expectedErr: errors.New("failed to find field no-field in item unsplash.com"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErr := tc.client.GetFieldOnItem(tc.itemName, tc.fieldName)
			equalError(t, tc.expectedErr, actualErr)
			equal(t, tc.expected, actual)
		})
	}
}

func TestGetPassword(t *testing.T) {
	testCases := []struct {
		name        string
		client      *cliClient
		itemName    string
		expected    []byte
		expectedErr error
	}{
		{
			name: "basic case",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id1",
						Name: "unsplash.com",
						Fields: []Field{
							{
								Name:  "API Key",
								Value: "value1",
							},
							{
								Name:  "no name",
								Value: "value2",
							},
						},
						Login: &Login{Password: "yyy"},
					},
					{
						ID:   "id2",
						Name: "my-credentials",
						Attachments: []Attachment{
							{
								ID:       "a-id1",
								FileName: "secret.auto.vars",
							},
						},
					},
				},
			},
			itemName: "unsplash.com",
			expected: []byte("yyy"),
		},
		{
			name: "item not find",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id1",
						Name: "unsplash.com",
						Fields: []Field{
							{
								Name:  "API Key",
								Value: "value1",
							},
							{
								Name:  "no name",
								Value: "value2",
							},
						},
					},
					{
						ID:   "id2",
						Name: "my-credentials",
						Attachments: []Attachment{
							{
								ID:       "a-id1",
								FileName: "secret.auto.vars",
							},
						},
					},
				},
			},
			itemName:    "no-item",
			expectedErr: errors.New("failed to find password in item no-item"),
		},
		{
			name: "password not found",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id1",
						Name: "unsplash.com",
						Fields: []Field{
							{
								Name:  "API Key",
								Value: "value1",
							},
							{
								Name:  "no name",
								Value: "value2",
							},
						},
					},
					{
						ID:   "id2",
						Name: "my-credentials",
						Attachments: []Attachment{
							{
								ID:       "a-id1",
								FileName: "secret.auto.vars",
							},
						},
					},
				},
			},
			itemName:    "unsplash.com",
			expectedErr: errors.New("failed to find password in item unsplash.com"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErr := tc.client.GetPassword(tc.itemName)
			equalError(t, tc.expectedErr, actualErr)
			equal(t, tc.expected, actual)
		})
	}
}

func TestGetAttachmentOnItemToFile(t *testing.T) {
	file, err := ioutil.TempFile("", "attachmentName")
	if err != nil {
		t.Errorf("Failed to create tmp file.")
	}
	defer func() {
		if err := os.Remove(file.Name()); err != nil {
			t.Errorf("Failed to remove tmp file: %q", file.Name())
		}
	}()
	if err := ioutil.WriteFile(file.Name(), []byte(`bla`), 0755); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	client := &cliClient{
		session: "abc",
		savedItems: []Item{
			{ID: "id1", Name: "unsplash.com", Fields: []Field{{Name: "API Key", Value: "value1"}}},
			{ID: "id2", Name: "my-credentials", Attachments: []Attachment{{ID: "a-id1", FileName: "secret.auto.vars"}, {ID: "a-id2", FileName: ".awsred"}}},
		},
	}
	testCases := []struct {
		name           string
		responses      map[string]execResponse
		expectedCalls  [][]string
		itemName       string
		attachmentName string
		expected       []byte
		expectedErr    error
	}{
		{
			name: "basic case",
			responses: map[string]execResponse{
				fmt.Sprintf("--session abc get attachment a-id1 --itemid id2 --output %s", file.Name()): {
					out: []byte(file.Name()),
				},
			},
			expectedCalls: [][]string{
				{"--session", "abc", "get", "attachment", "a-id1", "--itemid", "id2", "--output", file.Name()},
			},
			itemName:       "my-credentials",
			attachmentName: "secret.auto.vars",
			expected:       []byte("bla"),
		},
		{
			name: "get attachment cmd err",
			responses: map[string]execResponse{
				fmt.Sprintf("--session abc get attachment a-id1 --itemid id2 --output %s", file.Name()): {
					err: errors.New("some err"),
				},
			},
			expectedCalls: [][]string{
				{"--session", "abc", "get", "attachment", "a-id1", "--itemid", "id2", "--output", file.Name()},
			},
			itemName:       "my-credentials",
			attachmentName: "secret.auto.vars",
			expectedErr:    errors.New("some err"),
		},
		{
			name:           "item not found",
			expectedCalls:  [][]string{},
			itemName:       "no-item",
			attachmentName: "secret.auto.vars",
			expectedErr:    errors.New("failed to find attachment secret.auto.vars in item no-item"),
		},
		{
			name:           "attachment not found",
			expectedCalls:  [][]string{},
			itemName:       "my-credentials",
			attachmentName: "no attachment",
			expectedErr:    errors.New("failed to find attachment no attachment in item my-credentials"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := fakeExecutor{
				records:   [][]string{},
				responses: tc.responses,
			}
			client.run = e.Run
			actual, actualErr := client.getAttachmentOnItemToFile(tc.itemName, tc.attachmentName, file.Name())
			equalError(t, tc.expectedErr, actualErr)
			equal(t, tc.expected, actual)
			equal(t, tc.expectedCalls, e.records)
		})
	}
}

func TestLogout(t *testing.T) {
	client := &cliClient{}
	testCases := []struct {
		name          string
		responses     map[string]execResponse
		expectedCalls [][]string
		expected      []byte
		expectedErr   error
	}{
		{
			name: "basic case",
			responses: map[string]execResponse{
				"logout": {
					out: []byte(`You have logged out.
`),
				},
			},
			expectedCalls: [][]string{
				{"logout"},
			},
			expected: []byte(`You have logged out.
`),
		},
		{
			name: "some err",
			responses: map[string]execResponse{
				"logout": {
					err: errors.New("some err"),
				},
			},
			expectedCalls: [][]string{
				{"logout"},
			},
			expectedErr: fmt.Errorf("some err"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := fakeExecutor{
				records:   [][]string{},
				responses: tc.responses,
			}
			client.run = e.Run
			actual, actualErr := client.Logout()
			equalError(t, tc.expectedErr, actualErr)
			equal(t, tc.expected, actual)
			equal(t, tc.expectedCalls, e.records)
		})
	}
}

func TestSetFieldOnItem(t *testing.T) {
	client := &cliClient{}
	testCases := []struct {
		name               string
		client             *cliClient
		bwCliResponses     map[string]execResponse
		expectedCalls      [][]string
		itemName           string
		fieldName          string
		fieldValue         []byte
		expectedSavedItems []Item
		expectedErr        error
	}{
		{
			name: "edit an existing record",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id1",
						Name: "unsplash.com",
						Type: 1,
						Fields: []Field{
							{
								Name:  "API Key",
								Value: "value1",
							},
							{
								Name:  "no name",
								Value: "value2",
							},
						},
					},
					{
						ID:   "id2",
						Name: "my-credentials",
						Type: 1,
						Attachments: []Attachment{
							{
								ID:       "a-id1",
								FileName: "secret.auto.vars",
							},
						},
					},
				},
				session: "my-session",
			},
			bwCliResponses: map[string]execResponse{
				// below is the encoding for the JSON object for id1
				"--session my-session edit item id1 eyJpZCI6ImlkMSIsIm5hbWUiOiJ1bnNwbGFzaC5jb20iLCJ0eXBlIjoxLCJmaWVsZHMiOlt7Im5hbWUiOiJBUEkgS2V5IiwidmFsdWUiOiJuZXdfZmllbGRfdmFsdWUifSx7Im5hbWUiOiJubyBuYW1lIiwidmFsdWUiOiJ2YWx1ZTIifV0sImF0dGFjaG1lbnRzIjpudWxsfQ==": {
					out: []byte(`{"object":"item","id":"id3","type":1,"name":"autogen_item","login":{"password":null},"collectionIds":[],"revisionDate":"2020-07-31T19:45:56.746Z"}`),
				},
			},
			expectedCalls: [][]string{
				{"--session", "my-session", "edit", "item", "id1", "eyJpZCI6ImlkMSIsIm5hbWUiOiJ1bnNwbGFzaC5jb20iLCJ0eXBlIjoxLCJmaWVsZHMiOlt7Im5hbWUiOiJBUEkgS2V5IiwidmFsdWUiOiJuZXdfZmllbGRfdmFsdWUifSx7Im5hbWUiOiJubyBuYW1lIiwidmFsdWUiOiJ2YWx1ZTIifV0sImF0dGFjaG1lbnRzIjpudWxsfQ=="},
			},
			itemName:   "unsplash.com",
			fieldName:  "API Key",
			fieldValue: []byte("new_field_value"),
			expectedSavedItems: []Item{
				{
					ID:   "id1",
					Name: "unsplash.com",
					Type: 1,
					Fields: []Field{
						{
							Name:  "API Key",
							Value: "new_field_value",
						},
						{
							Name:  "no name",
							Value: "value2",
						},
					},
				},
				{
					ID:   "id2",
					Name: "my-credentials",
					Type: 1,
					Attachments: []Attachment{
						{
							ID:       "a-id1",
							FileName: "secret.auto.vars",
						},
					},
				},
			},
			expectedErr: nil,
		},
		{
			name: "add a new item",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id2",
						Name: "my-credentials",
						Type: 1,
						Attachments: []Attachment{
							{
								ID:       "a-id1",
								FileName: "secret.auto.vars",
							},
						},
					},
				},
				session: "my-session",
			},
			bwCliResponses: map[string]execResponse{
				"--session my-session create item eyJuYW1lIjoidW5zcGxhc2guY29tIiwidHlwZSI6MSwibG9naW4iOnt9LCJmaWVsZHMiOm51bGwsImF0dGFjaG1lbnRzIjpudWxsfQ==": {
					out: []byte(`{"id":"id1","type":1,"name":"unsplash.com","login":{"password":null}}`),
				},
				"--session my-session edit item id1 eyJpZCI6ImlkMSIsIm5hbWUiOiJ1bnNwbGFzaC5jb20iLCJ0eXBlIjoxLCJsb2dpbiI6e30sImZpZWxkcyI6W3sibmFtZSI6IkFQSSBLZXkiLCJ2YWx1ZSI6InZhbHVlMSJ9XSwiYXR0YWNobWVudHMiOm51bGx9": {
					out: []byte(`{"id":"id1","type":1,"name":"unsplash.com","login":{"password":null},"fields":[{"name":"API Key","value":"new_field_value"}]}`),
				},
			},
			expectedCalls: [][]string{
				{"--session", "my-session", "create", "item", "eyJuYW1lIjoidW5zcGxhc2guY29tIiwidHlwZSI6MSwibG9naW4iOnt9LCJmaWVsZHMiOm51bGwsImF0dGFjaG1lbnRzIjpudWxsfQ=="},
				{"--session", "my-session", "edit", "item", "id1", "eyJpZCI6ImlkMSIsIm5hbWUiOiJ1bnNwbGFzaC5jb20iLCJ0eXBlIjoxLCJsb2dpbiI6e30sImZpZWxkcyI6W3sibmFtZSI6IkFQSSBLZXkiLCJ2YWx1ZSI6InZhbHVlMSJ9XSwiYXR0YWNobWVudHMiOm51bGx9"},
			},
			itemName:   "unsplash.com",
			fieldName:  "API Key",
			fieldValue: []byte("value1"),
			expectedSavedItems: []Item{
				{
					ID:   "id2",
					Name: "my-credentials",
					Type: 1,
					Attachments: []Attachment{
						{
							ID:       "a-id1",
							FileName: "secret.auto.vars",
						},
					},
				},
				{
					ID:    "id1",
					Name:  "unsplash.com",
					Type:  1,
					Login: &Login{},
					Fields: []Field{
						{
							Name:  "API Key",
							Value: "value1",
						},
					},
				},
			},
			expectedErr: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := fakeExecutor{
				records:   [][]string{},
				responses: tc.bwCliResponses,
			}
			client = tc.client
			client.run = e.Run
			actualErr := client.SetFieldOnItem(tc.itemName, tc.fieldName, tc.fieldValue)
			equalError(t, tc.expectedErr, actualErr)
			equal(t, tc.expectedSavedItems, client.savedItems)
			equal(t, tc.expectedCalls, e.records)
		})
	}

}

func TestSetPassword(t *testing.T) {
	client := &cliClient{}
	testCases := []struct {
		name               string
		client             *cliClient
		bwCliResponses     map[string]execResponse
		expectedCalls      [][]string
		itemName           string
		newPassword        []byte
		expectedSavedItems []Item
		expectedErr        error
	}{
		{
			name: "edit an existing record",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:    "id1",
						Name:  "unsplash.com",
						Type:  1,
						Login: &Login{Password: "old_password"},
					},
				},
				session: "my-session",
			},
			bwCliResponses: map[string]execResponse{
				// below is the encoding for the JSON object for id1
				"--session my-session edit item id1 eyJpZCI6ImlkMSIsIm5hbWUiOiJ1bnNwbGFzaC5jb20iLCJ0eXBlIjoxLCJsb2dpbiI6eyJwYXNzd29yZCI6Im5ld19wYXNzd29yZCJ9LCJmaWVsZHMiOm51bGwsImF0dGFjaG1lbnRzIjpudWxsfQ==": {
					out: []byte(`{"object":"item","id":"id3","type":1,"name":"autogen_item","login":{"password":null},"collectionIds":[],"revisionDate":"2020-07-31T19:45:56.746Z"}`),
				},
			},
			expectedCalls: [][]string{
				{"--session", "my-session", "edit", "item", "id1", "eyJpZCI6ImlkMSIsIm5hbWUiOiJ1bnNwbGFzaC5jb20iLCJ0eXBlIjoxLCJsb2dpbiI6eyJwYXNzd29yZCI6Im5ld19wYXNzd29yZCJ9LCJmaWVsZHMiOm51bGwsImF0dGFjaG1lbnRzIjpudWxsfQ=="},
			},
			itemName:    "unsplash.com",
			newPassword: []byte("new_password"),
			expectedSavedItems: []Item{
				{
					ID:    "id1",
					Name:  "unsplash.com",
					Type:  1,
					Login: &Login{Password: "new_password"},
				},
			},
			expectedErr: nil,
		},
		{
			name: "add a new item",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id2",
						Name: "my-credentials",
						Type: 1,
						Attachments: []Attachment{
							{
								ID:       "a-id1",
								FileName: "secret.auto.vars",
							},
						},
					},
				},
				session: "my-session",
			},
			bwCliResponses: map[string]execResponse{
				"--session my-session create item eyJuYW1lIjoidW5zcGxhc2guY29tIiwidHlwZSI6MSwibG9naW4iOnsicGFzc3dvcmQiOiJuZXdfcGFzc3dvcmQifSwiZmllbGRzIjpudWxsLCJhdHRhY2htZW50cyI6bnVsbH0=": {
					out: []byte(`{"id":"id1","type":1,"name":"unsplash.com","login":{"password":"new_password"}}`),
				},
			},
			expectedCalls: [][]string{
				{"--session", "my-session", "create", "item", "eyJuYW1lIjoidW5zcGxhc2guY29tIiwidHlwZSI6MSwibG9naW4iOnsicGFzc3dvcmQiOiJuZXdfcGFzc3dvcmQifSwiZmllbGRzIjpudWxsLCJhdHRhY2htZW50cyI6bnVsbH0="},
			},
			itemName:    "unsplash.com",
			newPassword: []byte("new_password"),
			expectedSavedItems: []Item{
				{
					ID:   "id2",
					Name: "my-credentials",
					Type: 1,
					Attachments: []Attachment{
						{
							ID:       "a-id1",
							FileName: "secret.auto.vars",
						},
					},
				},
				{
					ID:    "id1",
					Name:  "unsplash.com",
					Type:  1,
					Login: &Login{Password: "new_password"},
				},
			},
			expectedErr: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := fakeExecutor{
				records:   [][]string{},
				responses: tc.bwCliResponses,
			}
			client = tc.client
			client.run = e.Run
			actualErr := client.SetPassword(tc.itemName, tc.newPassword)
			equalError(t, tc.expectedErr, actualErr)
			equal(t, tc.expectedSavedItems, client.savedItems)
			equal(t, tc.expectedCalls, e.records)
		})
	}

}

func TestSetAttachment(t *testing.T) {
	client := &cliClient{}
	testCases := []struct {
		name               string
		client             *cliClient
		bwCliResponses     map[string]execResponse
		expectedCalls      [][]string
		itemName           string
		fileName           string
		fileContents       []byte
		expectedSavedItems []Item
		expectedErr        error
	}{
		{
			name: "edit an existing record",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id1",
						Name: "unsplash.com",
						Type: 1,
						Attachments: []Attachment{
							{
								ID:       "attachmentID1",
								FileName: "File1",
							},
						},
					},
				},
				session: "my-session",
			},
			bwCliResponses: map[string]execResponse{
				"--session my-session get attachment attachmentID1 --itemid id1 --output ": {
					out: []byte(`dont-care`),
				},
				"--session my-session delete attachment attachmentID1 --itemid id1": {
					out: []byte(`{result:"true"}`),
				},
				"--session my-session create attachment --itemid id1 --file ": {
					out: []byte(`{"object":"attachment","id":"attachmentID2","filename":"File1"}`),
				},
			},
			expectedCalls: [][]string{
				{"--session", "my-session", "get", "attachment", "attachmentID1", "--itemid", "id1", "--output"},
				{"--session", "my-session", "delete", "attachment", "attachmentID1", "--itemid", "id1"},
				{"--session", "my-session", "create", "attachment", "--itemid", "id1", "--file"},
			},
			itemName:     "unsplash.com",
			fileName:     "File1",
			fileContents: []byte("new_file_contents"),
			expectedSavedItems: []Item{
				{
					ID:   "id1",
					Name: "unsplash.com",
					Type: 1,
					Attachments: []Attachment{
						{
							ID:       "attachmentID2",
							FileName: "File1",
						},
					},
				},
			},
			expectedErr: nil,
		},
		{
			name: "add a new item",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id2",
						Name: "my-credentials",
						Type: 1,
						Attachments: []Attachment{
							{
								ID:       "a-id1",
								FileName: "secret.auto.vars",
							},
						},
					},
				},
				session: "my-session",
			},
			bwCliResponses: map[string]execResponse{
				"--session my-session create item eyJuYW1lIjoidW5zcGxhc2guY29tIiwidHlwZSI6MSwibG9naW4iOnt9LCJmaWVsZHMiOm51bGwsImF0dGFjaG1lbnRzIjpudWxsfQ==": {
					out: []byte(`{"id":"id1","type":1,"name":"unsplash.com"}`),
				},
				"--session my-session create attachment --itemid id1 --file ": {
					out: []byte(`{"object":"attachment","id":"attachmentID2","filename":"File1"}`),
				},
			},
			expectedCalls: [][]string{
				{"--session", "my-session", "create", "item", "eyJuYW1lIjoidW5zcGxhc2guY29tIiwidHlwZSI6MSwibG9naW4iOnt9LCJmaWVsZHMiOm51bGwsImF0dGFjaG1lbnRzIjpudWxsfQ=="},
				{"--session", "my-session", "create", "attachment", "--itemid", "id1", "--file"},
			},
			itemName:     "unsplash.com",
			fileName:     "file2",
			fileContents: []byte("new_file_contents"),
			expectedSavedItems: []Item{
				{
					ID:   "id2",
					Name: "my-credentials",
					Type: 1,
					Attachments: []Attachment{
						{
							ID:       "a-id1",
							FileName: "secret.auto.vars",
						},
					},
				},
				{
					ID:   "id1",
					Name: "unsplash.com",
					Type: 1,
					Attachments: []Attachment{
						{
							ID:       "attachmentID2",
							FileName: "File1",
						},
					},
				},
			},
			expectedErr: nil,
		},
		{
			name: "replace an existing attachment when multiple attachments exist",
			client: &cliClient{
				savedItems: []Item{
					{
						ID:   "id1",
						Name: "unsplash.com",
						Type: 1,
						Attachments: []Attachment{
							{
								ID:       "attachmentID1",
								FileName: "File1",
							},
							{
								ID:       "attachmentID2",
								FileName: "File2",
							},
						},
					},
				},
				session: "my-session",
			},
			bwCliResponses: map[string]execResponse{
				"--session my-session get attachment attachmentID1 --itemid id1 --output ": {
					out: []byte(`dont-care`),
				},
				"--session my-session delete attachment attachmentID1 --itemid id1": {
					out: []byte(`{result:"true"}`),
				},
				"--session my-session create attachment --itemid id1 --file ": {
					out: []byte(`{"object":"attachment","id":"attachmentID3","filename":"File1"}`),
				},
			},
			expectedCalls: [][]string{
				{"--session", "my-session", "get", "attachment", "attachmentID1", "--itemid", "id1", "--output"},
				{"--session", "my-session", "delete", "attachment", "attachmentID1", "--itemid", "id1"},
				{"--session", "my-session", "create", "attachment", "--itemid", "id1", "--file"},
			},
			itemName:     "unsplash.com",
			fileName:     "File1",
			fileContents: []byte("new_file_contents"),
			expectedSavedItems: []Item{
				{
					ID:   "id1",
					Name: "unsplash.com",
					Type: 1,
					Attachments: []Attachment{
						{
							ID:       "attachmentID2",
							FileName: "File2",
						},
						{
							ID:       "attachmentID3",
							FileName: "File1",
						},
					},
				},
			},
			expectedErr: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := fakeExecutor{
				records:   [][]string{},
				responses: tc.bwCliResponses,
			}
			client = tc.client
			client.run = e.RunIgnoringFiles
			actualErr := client.SetAttachmentOnItem(tc.itemName, tc.fileName, tc.fileContents)
			equalError(t, tc.expectedErr, actualErr)
			equal(t, tc.expectedSavedItems, client.savedItems)
			equal(t, tc.expectedCalls, e.records)
		})
	}
}

type execResponse struct {
	out []byte
	err error
}

// fakeExecutor is useful in testing for mocking an Executor
type fakeExecutor struct {
	records   [][]string
	responses map[string]execResponse
}

func (e *fakeExecutor) RunIgnoringFiles(args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	slashIndex := strings.Index(key, "/")
	if slashIndex != -1 {
		if err := ioutil.WriteFile(key[slashIndex:], []byte("attachment_contents"), 0644); err != nil {
			return nil, fmt.Errorf("failed to create temporary file attachment: %v", err)
		}
		key = key[:slashIndex]
		e.records = append(e.records, args[:len(args)-1])
	} else {
		e.records = append(e.records, args)
	}
	if response, ok := e.responses[key]; ok {
		return response.out, response.err
	}
	return []byte{}, fmt.Errorf("no response configured for %s", key)
}

func (e *fakeExecutor) Run(args ...string) ([]byte, error) {
	e.records = append(e.records, args)
	key := strings.Join(args, " ")
	if response, ok := e.responses[key]; ok {
		return response.out, response.err
	}
	return []byte{}, fmt.Errorf("no response configured for %s", key)
}

func equalError(t *testing.T, expected, actual error) {
	if expected != nil && actual == nil || expected == nil && actual != nil {
		t.Errorf("expecting error %v, got %v", expected, actual)
	}
	if expected != nil && actual != nil && expected.Error() != actual.Error() {
		t.Errorf("expecting error msg %q, got %q", expected.Error(), actual.Error())
	}
}

func equal(t *testing.T, expected, actual interface{}) {
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("actual differs from expected:\n%s", cmp.Diff(expected, actual))
	}
}
