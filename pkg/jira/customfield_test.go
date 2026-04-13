package jira

import (
	"context"
	"reflect"
	"testing"

	jiraapi "github.com/andygrunwald/go-jira"
)

func TestValueByID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		issue   *jiraapi.Issue
		fieldID string
		want    any
	}{
		{
			name:    "nil issue",
			issue:   nil,
			fieldID: "customfield_123",
			want:    nil,
		},
		{
			name:    "nil Fields",
			issue:   &jiraapi.Issue{},
			fieldID: "customfield_123",
			want:    nil,
		},
		{
			name:    "nil Unknowns",
			issue:   &jiraapi.Issue{Fields: &jiraapi.IssueFields{}},
			fieldID: "customfield_123",
			want:    nil,
		},
		{
			name: "empty fieldID",
			issue: &jiraapi.Issue{
				Fields: &jiraapi.IssueFields{Unknowns: map[string]interface{}{"customfield_123": "val"}},
			},
			fieldID: "",
			want:    nil,
		},
		{
			name: "found",
			issue: &jiraapi.Issue{
				Fields: &jiraapi.IssueFields{
					Unknowns: map[string]interface{}{"customfield_123": "qa-user"},
				},
			},
			fieldID: "customfield_123",
			want:    "qa-user",
		},
		{
			name: "not found",
			issue: &jiraapi.Issue{
				Fields: &jiraapi.IssueFields{
					Unknowns: map[string]interface{}{"customfield_999": "other"},
				},
			},
			fieldID: "customfield_123",
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValueByID(tt.issue, tt.fieldID)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ValueByID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValueByID_with_fallback_id(t *testing.T) {
	t.Parallel()
	// ValueByID is used with a known custom field ID (e.g. customfield_12316243).
	// SetFallbackIDs can map names to these IDs when name-based API lookup is empty.
	issue := &jiraapi.Issue{
		Fields: &jiraapi.IssueFields{
			Unknowns: map[string]interface{}{"customfield_12316243": "qa@example.com"},
		},
	}
	got := ValueByID(issue, "customfield_12316243")
	if !reflect.DeepEqual(got, "qa@example.com") {
		t.Errorf("ValueByID = %v, want qa@example.com", got)
	}
}

// TestFieldID_and_Value_use_fallback exercises the fallback path when the field list
// is loaded but the name is not in the API response (e.g. instance-specific custom field).
func TestFieldID_and_Value_use_fallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Resolver with no client, already "loaded" with empty byName so we skip API and use fallback.
	r := &CustomFieldResolver{
		client:      nil,
		byName:      map[string]string{},
		loaded:      true,
		fallbackIDs: map[string]string{"QA Contact": "customfield_12316243"},
	}
	id, err := r.FieldID(ctx, "QA Contact")
	if err != nil {
		t.Fatalf("FieldID() error = %v", err)
	}
	if id != "customfield_12316243" {
		t.Errorf("FieldID() = %q, want customfield_12316243", id)
	}
	issue := &jiraapi.Issue{
		Fields: &jiraapi.IssueFields{
			Unknowns: map[string]interface{}{"customfield_12316243": "qa@example.com"},
		},
	}
	val, err := r.Value(ctx, issue, "QA Contact")
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	if !reflect.DeepEqual(val, "qa@example.com") {
		t.Errorf("Value() = %v, want qa@example.com", val)
	}
}

// TestFieldID_nil_client_returns_error ensures we return an error instead of panicking.
func TestFieldID_nil_client_returns_error(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := NewCustomFieldResolver(nil)
	r.SetFallbackIDs(map[string]string{"QA Contact": "customfield_12316243"})
	_, err := r.FieldID(ctx, "QA Contact")
	if err == nil {
		t.Error("FieldID() expected error when client is nil")
	}
}
