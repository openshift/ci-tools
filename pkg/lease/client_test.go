package lease

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestAcquire(t *testing.T) {
	ctx := context.Background()
	var calls []string
	client := NewFakeClient("owner", "url", 0, nil, &calls, nil)
	if _, err := client.Acquire("rtype", 1, ctx, nil); err != nil {
		t.Fatal(err)
	}
	expected := []string{"acquireWaitWithPriority owner rtype free leased random"}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("wrong calls to the boskos client: %v", cmp.Diff(calls, expected))
	}
	if err := client.Heartbeat(); err != nil {
		t.Fatal(err)
	}
	expected = []string{
		"acquireWaitWithPriority owner rtype free leased random",
		"updateone owner rtype_0 leased 0",
	}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("wrong calls to the boskos client: %v", cmp.Diff(calls, expected))
	}
	list, err := client.ReleaseAll()
	if err != nil {
		t.Fatal(err)
	}
	expected = []string{"rtype_0"}
	if !reflect.DeepEqual(list, expected) {
		t.Fatalf("wrong list: %v", cmp.Diff(list, expected))
	}
}

func TestHeartbeatCancel(t *testing.T) {
	ctx := context.Background()
	var calls []string
	client := NewFakeClient("owner", "url", 0, map[string]error{"updateone owner rtype_0 leased 0": errors.New("injected error")}, &calls, nil)
	var called bool
	if _, err := client.Acquire("rtype", 1, ctx, func() { called = true }); err != nil {
		t.Fatal(err)
	}
	if err := client.Heartbeat(); err == nil {
		t.Fatal("Heartbeat() did not fail")
	}
	if !called {
		t.Fatal("cancel function not called")
	}
}

func TestHeartbeatRetries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		success  bool
		requests int
		failures map[string]error
	}{{
		name:     "requests == retries, should succeed",
		requests: 3,
		success:  true,
		failures: map[string]error{
			"updateone owner rtype_0 leased 0": errors.New("injected error"),
			"updateone owner rtype_0 leased 1": errors.New("injected error"),
		},
	}, {
		name:     "requests < retries, should fail",
		requests: 3,
		failures: map[string]error{
			"updateone owner rtype_0 leased 0": errors.New("injected error"),
			"updateone owner rtype_0 leased 1": errors.New("injected error"),
			"updateone owner rtype_0 leased 2": errors.New("injected error"),
		},
	}, {
		name:     "requests > retries with intermittent failures, should succeed",
		success:  true,
		requests: 6,
		failures: map[string]error{
			"updateone owner rtype_0 leased 0": errors.New("injected error"),
			"updateone owner rtype_0 leased 1": errors.New("injected error"),
			"updateone owner rtype_0 leased 3": errors.New("injected error"),
			"updateone owner rtype_0 leased 4": errors.New("injected error"),
		},
	}, {
		name:     "requests <= retries with intermittent failures, should fail",
		requests: 6,
		failures: map[string]error{
			"updateone owner rtype_0 leased 0": errors.New("injected error"),
			"updateone owner rtype_0 leased 1": errors.New("injected error"),
			"updateone owner rtype_0 leased 3": errors.New("injected error"),
			"updateone owner rtype_0 leased 4": errors.New("injected error"),
			"updateone owner rtype_0 leased 5": errors.New("injected error"),
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var calls []string
			client := NewFakeClient("owner", "url", 2, tc.failures, &calls, nil)
			var called bool
			if _, err := client.Acquire("rtype", 1, ctx, func() { called = true }); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tc.requests-1; i++ {
				if err := client.Heartbeat(); err != nil {
					t.Errorf("unexpected error (%d): %v", i, err)
				}
			}
			err := client.Heartbeat()
			if tc.success {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if called {
					t.Error("cancel function unexpectedly called")
				}
			} else {
				if err == nil {
					t.Errorf("unexpected success")
				}
				if !called {
					t.Error("cancel function not called")
				}
			}
		})
	}
}

func TestLeases(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		acquire    []string
		release    []string
		wantLeases []string
	}{
		{
			name:       "Acquire several leases",
			acquire:    []string{"a", "b", "c"},
			wantLeases: []string{"a_0", "b_1", "c_2"},
		},
		{
			name:       "Acquire and release several leases",
			acquire:    []string{"a", "b", "c"},
			release:    []string{"a_0", "b_1"},
			wantLeases: []string{"c_2"},
		},
		{
			name:    "Acquire no leases",
			acquire: []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.TODO()
			client := NewFakeClient("owner", "url", 0, nil, new([]string), nil)
			for _, l := range tc.acquire {
				if _, err := client.Acquire(l, 1, ctx, nil); err != nil {
					t.Fatalf("acquire: %s", err)
				}
			}

			for _, l := range tc.release {
				if err := client.Release(l); err != nil {
					t.Fatalf("release: %s", err)
				}
			}

			gotLeases := client.Leases()
			if diff := cmp.Diff(tc.wantLeases, gotLeases); diff != "" {
				t.Errorf("unexpected leases: %s", diff)
			}
		})
	}

}
