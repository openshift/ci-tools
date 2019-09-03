package steps

import (
	"sync"
	"testing"

	coreapi "k8s.io/api/core/v1"

	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDryLogger(t *testing.T) {
	var wg sync.WaitGroup
	dryLogger := &DryLogger{}
	pod := &coreapi.Pod{
		TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
		Spec:       coreapi.PodSpec{Containers: []coreapi.Container{{Name: "test"}}},
	}

	addObjectFn := func() {
		defer wg.Done()
		dryLogger.AddObject(pod.DeepCopyObject())
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go addObjectFn()
	}

	wg.Wait()
	objectList := dryLogger.GetObjects()
	if len(objectList) != 10 {
		t.Fatalf("ten objects expected to be in the list. got %d: %#v", len(objectList), objectList)
	}
}
