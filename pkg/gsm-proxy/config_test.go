package gsmproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

const testOwnership = "groups:\n  team-a:\n    secret_collections: [foo]\n"

func TestNewReloadingCollectionAuthorizer(t *testing.T) {
	t.Run("startup validation", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			content string
			missing bool
		}{
			{name: "missing file", missing: true},
			{name: "malformed YAML", content: "groups: ["},
			{name: "unknown field", content: "unexpected: sentinel-must-not-leak"},
			{name: "invalid collection", content: "groups:\n  team-a:\n    secret_collections: [Bad_Name_]\n"},
			{name: "duplicate collection", content: "groups:\n  team-a:\n    secret_collections: [foo, foo]\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "ownership.yaml")
				if !tc.missing {
					writeOwnership(t, path, tc.content)
				}
				log, events := ownershipTestLogger()
				authorizer, err := NewReloadingCollectionAuthorizer(t.Context(), path, log)
				if err == nil || authorizer != nil {
					t.Fatal("invalid initial configuration must prevent startup")
				}
				if strings.Contains(err.Error(), "sentinel-must-not-leak") {
					t.Fatal("startup error contains configuration contents")
				}
				assertOwnershipLogsSafe(t, events)
			})
		}
	})

	t.Run("reload requests", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ownership.yaml")
		writeOwnership(t, path, testOwnership)
		log, events := ownershipTestLogger()
		authorizer, err := NewReloadingCollectionAuthorizer(t.Context(), path, log)
		if err != nil {
			t.Fatal(err)
		}
		var nextCalls int
		handler := NewServeMux(RequireCollectionAccess(
			&fakeVerifier{id: Identity{Username: "test-user", Groups: []string{"team-a"}}},
			authorizer, log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalls++
				w.WriteHeader(http.StatusNoContent)
			}),
		))
		request := func() int {
			t.Helper()
			r := httptest.NewRequest(http.MethodGet, "/api/v1/collections/foo/secrets", nil)
			r.Header.Set("Authorization", "Bearer same-token")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return w.Code
		}
		if got := request(); got != http.StatusNoContent {
			t.Fatalf("initial request status = %d", got)
		}

		for _, tc := range []struct {
			name    string
			content string
			status  int
		}{
			{name: "ownership revoked", content: "groups:\n  team-a:\n    secret_collections: [bar]\n", status: http.StatusForbidden},
			{name: "ownership granted", content: testOwnership, status: http.StatusNoContent},
			{name: "unclaimed owner", content: "groups:\n  team-a:\n    unclaimed: true\n    secret_collections: [foo]\n", status: http.StatusForbidden},
			{name: "reclaimed owner", content: testOwnership, status: http.StatusNoContent},
			{name: "empty ownership", content: "groups: {}\n", status: http.StatusForbidden},
			{name: "restore ownership", content: testOwnership, status: http.StatusNoContent},
		} {
			t.Run(tc.name, func(t *testing.T) {
				replaceOwnership(t, path, tc.content)
				waitForOwnership(t, func() bool { return request() == tc.status })
				before := nextCalls
				if got := request(); got != tc.status {
					t.Fatalf("request status = %d, want %d", got, tc.status)
				}
				if tc.status == http.StatusForbidden && nextCalls != before {
					t.Fatal("revoked request reached the protected handler")
				}
			})
		}

		for _, content := range []string{
			"groups: [sentinel-must-not-leak",
			"unexpected: sentinel-must-not-leak",
			"groups:\n  team-a:\n    secret_collections: [Bad_Name_]\n",
		} {
			drainOwnershipEvents(events)
			replaceOwnership(t, path, content)
			waitForOwnershipError(t, events)
			if got := request(); got != http.StatusNoContent {
				t.Fatalf("invalid replacement discarded last valid grant: status %d", got)
			}
		}

		replaceOwnership(t, path, "groups: {}\n")
		waitForOwnership(t, func() bool { return request() == http.StatusForbidden })
		replaceOwnership(t, path, testOwnership)
		waitForOwnership(t, func() bool { return request() == http.StatusNoContent })
		assertOwnershipLogsSafe(t, events)
	})

	t.Run("unreadable update", func(t *testing.T) {
		// Test removal independently of replacement bursts: filesystem notifications
		// can coalesce intermediate states during rapid replacement and removal.
		path := filepath.Join(t.TempDir(), "ownership.yaml")
		writeOwnership(t, path, testOwnership)
		log, events := ownershipTestLogger()
		authorizer, err := NewReloadingCollectionAuthorizer(t.Context(), path, log)
		if err != nil {
			t.Fatal(err)
		}
		identity := Identity{Groups: []string{"team-a"}}
		allowed := func() bool { return authorizer.Authorize(identity, "foo") == nil }
		drainOwnershipEvents(events)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		waitForOwnershipError(t, events)
		if !allowed() {
			t.Fatal("unreadable replacement discarded last valid grant")
		}
		replaceOwnership(t, path, "groups: {}\n")
		waitForOwnership(t, func() bool { return !allowed() })
		replaceOwnership(t, path, testOwnership)
		waitForOwnership(t, allowed)
	})

	t.Run("in-place update", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ownership.yaml")
		writeOwnership(t, path, testOwnership)
		log, _ := ownershipTestLogger()
		authorizer, err := NewReloadingCollectionAuthorizer(t.Context(), path, log)
		if err != nil {
			t.Fatal(err)
		}
		identity := Identity{Groups: []string{"team-a"}}
		writeOwnership(t, path, "groups: {}\n")
		waitForOwnership(t, func() bool { return errors.Is(authorizer.Authorize(identity, "foo"), ErrForbidden) })
		writeOwnership(t, path, testOwnership)
		waitForOwnership(t, func() bool { return authorizer.Authorize(identity, "foo") == nil })
	})

	t.Run("ConfigMap rotation", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, "ownership.yaml")
		rotate := func(version, content string) {
			t.Helper()
			versionDir := filepath.Join(directory, version)
			if err := os.Mkdir(versionDir, 0700); err != nil {
				t.Fatal(err)
			}
			writeOwnership(t, filepath.Join(versionDir, "ownership.yaml"), content)
			temporary := filepath.Join(directory, "..data_tmp")
			if err := os.Symlink(version, temporary); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(temporary, filepath.Join(directory, "..data")); err != nil {
				t.Fatal(err)
			}
		}
		rotate("..version1", testOwnership)
		if err := os.Symlink(filepath.Join("..data", "ownership.yaml"), path); err != nil {
			t.Fatal(err)
		}
		log, events := ownershipTestLogger()
		authorizer, err := NewReloadingCollectionAuthorizer(t.Context(), path, log)
		if err != nil {
			t.Fatal(err)
		}
		identity := Identity{Groups: []string{"team-a"}}
		allowed := func() bool { return authorizer.Authorize(identity, "foo") == nil }
		if !allowed() {
			t.Fatal("initial ConfigMap did not grant access")
		}
		rotate("..version2", "groups: {}\n")
		waitForOwnership(t, func() bool { return !allowed() })
		rotate("..version3", testOwnership)
		waitForOwnership(t, allowed)
		drainOwnershipEvents(events)
		rotate("..version4", "unexpected: sentinel-must-not-leak")
		waitForOwnershipError(t, events)
		if !allowed() {
			t.Fatal("invalid ConfigMap discarded last valid grant")
		}
		rotate("..version5", "groups: {}\n")
		waitForOwnership(t, func() bool { return !allowed() })
		assertOwnershipLogsSafe(t, events)
	})

	t.Run("concurrent authorization", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ownership.yaml")
		writeOwnership(t, path, testOwnership)
		log, _ := ownershipTestLogger()
		authorizer, err := NewReloadingCollectionAuthorizer(t.Context(), path, log)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				for ctx.Err() == nil {
					err := authorizer.Authorize(Identity{Groups: []string{"team-a"}}, "foo")
					if err != nil && !errors.Is(err, ErrForbidden) {
						t.Errorf("unexpected authorization error: %v", err)
						return
					}
				}
			})
		}
		defer func() {
			cancel()
			workers.Wait()
		}()
		for i := range 10 {
			content := testOwnership
			wantAllowed := i%2 == 0
			if !wantAllowed {
				content = "groups: {}\n"
			}
			replaceOwnership(t, path, content)
			waitForOwnership(t, func() bool {
				return (authorizer.Authorize(Identity{Groups: []string{"team-a"}}, "foo") == nil) == wantAllowed
			})
		}
	})
}

func writeOwnership(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func replaceOwnership(t *testing.T, path, content string) {
	t.Helper()
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ownership-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.WriteString(content); err != nil {
		temporary.Close()
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		t.Fatal(err)
	}
}

func waitForOwnership(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-deadline.C:
			t.Fatal("ownership change was not observed")
		case <-ticker.C:
		}
	}
}

type ownershipLogHook struct {
	events chan string
}

func (h ownershipLogHook) Levels() []logrus.Level { return logrus.AllLevels }
func (h ownershipLogHook) Fire(entry *logrus.Entry) error {
	message, err := entry.String()
	if err != nil {
		return err
	}
	select {
	case h.events <- message:
	default:
	}
	return nil
}

func ownershipTestLogger() (*logrus.Entry, chan string) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	log.SetLevel(logrus.TraceLevel)
	events := make(chan string, 1024)
	log.AddHook(ownershipLogHook{events: events})
	return logrus.NewEntry(log), events
}

func drainOwnershipEvents(events chan string) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}

func waitForOwnershipError(t *testing.T, events chan string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if strings.Contains(event, "sentinel-must-not-leak") {
				t.Fatal("configuration contents leaked into logs")
			}
			if strings.Contains(event, "keeping last valid configuration") {
				return
			}
		case <-deadline.C:
			t.Fatal("reload failure was not reported")
		}
	}
}

func assertOwnershipLogsSafe(t *testing.T, events chan string) {
	t.Helper()
	for {
		select {
		case event := <-events:
			if strings.Contains(event, "sentinel-must-not-leak") {
				t.Fatal("configuration contents leaked into logs")
			}
		default:
			return
		}
	}
}
