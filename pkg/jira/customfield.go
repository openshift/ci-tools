package jira

import (
	"context"
	"errors"
	"maps"
	"sync"

	jiraapi "github.com/andygrunwald/go-jira"

	jirautil "sigs.k8s.io/prow/pkg/jira"
)

// CustomFieldResolver resolves field names to IDs; fetches the field list once and caches.
// Use SetFallbackIDs to supply name→ID when API lookup is empty (e.g. instance-specific IDs).
type CustomFieldResolver struct {
	client      *jiraapi.Client
	mu          sync.RWMutex
	byName      map[string]string
	loaded      bool
	fallbackIDs map[string]string // optional: field name -> custom field ID
}

func NewCustomFieldResolver(client *jiraapi.Client) *CustomFieldResolver {
	return &CustomFieldResolver{
		client:      client,
		byName:      make(map[string]string),
		fallbackIDs: make(map[string]string),
	}
}

// SetFallbackIDs sets the optional name→custom field ID map (e.g. "QA Contact" -> "customfield_12316243").
// Safe to call concurrently; replaces the previous map with a copy.
func (r *CustomFieldResolver) SetFallbackIDs(ids map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ids == nil {
		r.fallbackIDs = make(map[string]string)
		return
	}
	r.fallbackIDs = maps.Clone(ids)
}

func (r *CustomFieldResolver) loadFields(ctx context.Context) error {
	r.mu.RLock()
	if r.loaded {
		r.mu.RUnlock()
		return nil
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return nil
	}
	if r.client == nil {
		return errors.New("jira client is nil")
	}
	fields, resp, err := r.client.Field.GetListWithContext(ctx)
	if err != nil {
		return jirautil.HandleJiraError(resp, err)
	}
	for _, f := range fields {
		r.byName[f.Name] = f.ID
	}
	r.loaded = true
	return nil
}

// FieldID returns the field ID for name, or "" if not found.
// If name lookup is empty and SetFallbackIDs supplied that name, that ID is returned.
func (r *CustomFieldResolver) FieldID(ctx context.Context, fieldName string) (string, error) {
	if err := r.loadFields(ctx); err != nil {
		return "", err
	}
	r.mu.RLock()
	id := r.byName[fieldName]
	if id == "" {
		id = r.fallbackIDs[fieldName]
	}
	r.mu.RUnlock()
	return id, nil
}

// Value returns the custom field value for the issue by field name.
func (r *CustomFieldResolver) Value(ctx context.Context, issue *jiraapi.Issue, fieldName string) (any, error) {
	if issue == nil || issue.Fields == nil {
		return nil, nil
	}
	id, err := r.FieldID(ctx, fieldName)
	if err != nil || id == "" {
		return nil, err
	}
	return ValueByID(issue, id), nil
}

// ValueByID returns the custom field value for the issue by raw field ID (e.g. customfield_12345).
// Use when name-based resolution is not available or as a temp override.
func ValueByID(issue *jiraapi.Issue, fieldID string) any {
	if issue == nil || issue.Fields == nil || issue.Fields.Unknowns == nil || fieldID == "" {
		return nil
	}
	if v, ok := issue.Fields.Unknowns[fieldID]; ok {
		return v
	}
	return nil
}
