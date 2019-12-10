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
	client := NewFakeClient("owner", "url", nil, &calls)
	if _, err := client.Acquire("rtype", ctx, nil); err != nil {
		t.Fatal(err)
	}
	expected := []string{"acquire owner rtype free leased"}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("wrong calls to the boskos client: %v", diff.ObjectDiff(calls, expected))
	}
	if err := client.Heartbeat(); err != nil {
		t.Fatal(err)
	}
	expected = []string{
		"acquire owner rtype free leased",
		"updateone owner rtype0 leased",
	}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("wrong calls to the boskos client: %v", diff.ObjectDiff(calls, expected))
	}
	list, err := client.ReleaseAll()
	if err != nil {
		t.Fatal(err)
	}
	expected = []string{"rtype0"}
	if !reflect.DeepEqual(list, expected) {
		t.Fatalf("wrong list: %v", diff.ObjectDiff(list, expected))
	}
}

func TestHeartbeatCancel(t *testing.T) {
	ctx := context.Background()
	var calls []string
	client := NewFakeClient("owner", "url", sets.NewString("updateone owner rtype0 leased"), &calls)
	var called bool
	if _, err := client.Acquire("rtype", ctx, func() { called = true }); err != nil {
		t.Fatal(err)
	}
	if err := client.Heartbeat(); err == nil {
		t.Fatal("Heartbeat() did not fail")
	}
	if !called {
		t.Fatal("cancel function not called")
	}
}
