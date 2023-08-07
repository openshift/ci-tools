package lease

import (
	"context"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestAcquire(t *testing.T) {
	ctx := context.Background()
	var calls []string
	client := NewFakeClient("owner", "url", 0, nil, &calls)
	if _, err := client.Acquire("rtype", 1, ctx, nil); err != nil {
		t.Fatal(err)
	}
	expected := []string{"acquire owner rtype free leased random"}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("wrong calls to the boskos client: %v", diff.ObjectDiff(calls, expected))
	}
	if err := client.Heartbeat(); err != nil {
		t.Fatal(err)
	}
	expected = []string{
		"acquire owner rtype free leased random",
		"updateone owner rtype_0 leased 0",
	}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("wrong calls to the boskos client: %v", diff.ObjectDiff(calls, expected))
	}
	list, err := client.ReleaseAll()
	if err != nil {
		t.Fatal(err)
	}
	expected = []string{"rtype_0"}
	if !reflect.DeepEqual(list, expected) {
		t.Fatalf("wrong list: %v", diff.ObjectDiff(list, expected))
	}
}

func TestHeartbeatCancel(t *testing.T) {
	ctx := context.Background()
	var calls []string
	client := NewFakeClient("owner", "url", 0, sets.New[string]("updateone owner rtype_0 leased 0"), &calls)
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
		failures []string
	}{{
		name:     "requests == retries, should succeed",
		requests: 3,
		success:  true,
		failures: []string{
			"updateone owner rtype_0 leased 0",
			"updateone owner rtype_0 leased 1",
		},
	}, {
		name:     "requests < retries, should fail",
		requests: 3,
		failures: []string{
			"updateone owner rtype_0 leased 0",
			"updateone owner rtype_0 leased 1",
			"updateone owner rtype_0 leased 2",
		},
	}, {
		name:     "requests > retries with intermittent failures, should succeed",
		success:  true,
		requests: 6,
		failures: []string{
			"updateone owner rtype_0 leased 0",
			"updateone owner rtype_0 leased 1",
			"updateone owner rtype_0 leased 3",
			"updateone owner rtype_0 leased 4",
		},
	}, {
		name:     "requests <= retries with intermittent failures, should fail",
		requests: 6,
		failures: []string{
			"updateone owner rtype_0 leased 0",
			"updateone owner rtype_0 leased 1",
			"updateone owner rtype_0 leased 3",
			"updateone owner rtype_0 leased 4",
			"updateone owner rtype_0 leased 5",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var calls []string
			client := NewFakeClient("owner", "url", 2, sets.New[string](tc.failures...), &calls)
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
