package modals

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/slack-go/slack"
)

// ViewUpdate is a tuple holding a view update request and response
type ViewUpdate struct {
	ViewUpdateRequest
	ViewUpdateResponse
}

// ViewUpdateRequest describes a client call to update a view
type ViewUpdateRequest struct {
	View                     slack.ModalViewRequest
	ExternalID, Hash, ViewID string
}

// ViewUpdateResponse describes the response to the client call
type ViewUpdateResponse struct {
	Response *slack.ViewResponse
	Error    error
}

// FakeViewUpdater is a ViewUpdater with injectable behavior
type FakeViewUpdater struct {
	behavior []ViewUpdate
	unwanted []ViewUpdateRequest

	// we expect to be called once but we don't know when
	ctx    context.Context
	cancel context.CancelFunc
}

func (f *FakeViewUpdater) UpdateView(view slack.ModalViewRequest, externalID, hash, viewID string) (*slack.ViewResponse, error) {
	defer f.cancel()
	request := ViewUpdateRequest{
		View:       view,
		ExternalID: externalID,
		Hash:       hash,
		ViewID:     viewID,
	}
	var response ViewUpdate
	index := -1
	for i, entry := range f.behavior {
		if cmp.Equal(entry.ViewUpdateRequest, request) {
			index = i
			break
		}
	}
	if index == -1 {
		f.unwanted = append(f.unwanted, request)
		return nil, errors.New("no such issue request behavior in fake")
	}
	f.behavior = append(f.behavior[:index], f.behavior[index+1:]...)
	return response.Response, response.Error
}

var _ ViewUpdater = &FakeViewUpdater{}

// NewFake creates a new fake updater with the injected behavior
func NewFake(calls []ViewUpdate) *FakeViewUpdater {
	ctx, cancel := context.WithCancel(context.Background())
	return &FakeViewUpdater{behavior: calls, unwanted: []ViewUpdateRequest{}, ctx: ctx, cancel: cancel}
}

// Called allows a consumer to know we have been called
func (f *FakeViewUpdater) Called() context.Context {
	return f.ctx
}

// Validate ensures that all expected client calls happened
func (f *FakeViewUpdater) Validate(t *testing.T) {
	for _, request := range f.behavior {
		t.Errorf("fake view updater did not get request: %v", request)
	}
	for _, request := range f.unwanted {
		t.Errorf("fake view updater got unwanted request: %v", request)
	}
}
