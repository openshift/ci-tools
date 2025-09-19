package dispatcher

import (
	"errors"
	"testing"
	"time"
)

func TestErrOutWhenNoClusterIsAvailable(t *testing.T) {
	t.Parallel()

	s := NewEphemeralClusterDispatcher([]string{})

	_, err := s.Dispatch("foobar")
	if !errors.Is(err, errNoClusterAvailable) {
		t.Errorf("Expected error %T but got %T", errNoClusterAvailable, err)
	}
}

func TestDispatch(t *testing.T) {
	t.Parallel()

	s := NewEphemeralClusterDispatcher([]string{"b01", "b02", "b03"})

	for _, res := range []struct {
		jobName     string
		wantCluster string
	}{
		{jobName: "pj1", wantCluster: "b01"},
		{jobName: "pj2", wantCluster: "b02"},
		{jobName: "pj1", wantCluster: "b01"},
	} {
		c, err := s.Dispatch(res.jobName)
		if err != nil {
			t.Fatalf("unexpected error %s", err)
		}

		if res.wantCluster != c {
			t.Errorf("want cluster %s but got %s", res.wantCluster, c)
		}
	}
}

func TestCacheExpires(t *testing.T) {
	t.Parallel()

	s := NewEphemeralClusterDispatcher([]string{"b01", "b02", "b03"})
	now := time.Now()
	s.now = func() time.Time { return now }
	jobName := "foo"

	c, err := s.Dispatch(jobName)
	if err != nil {
		t.Fatalf("unexpected error %s", err)
	}
	if c != "b01" {
		t.Errorf("want b01 but got %s", c)
	}

	s.now = func() time.Time { return now.Add(cacheTTL + time.Nanosecond) }

	c, err = s.Dispatch(jobName)
	if err != nil {
		t.Fatalf("unexpected error %s", err)
	}
	if c != "b02" {
		t.Errorf("want b02 but got %s", c)
	}
}

func TestReset(t *testing.T) {
	t.Parallel()

	s := NewEphemeralClusterDispatcher([]string{"b01", "b02", "b03"})
	jobName := "foo"

	c, err := s.Dispatch(jobName)
	if err != nil {
		t.Fatalf("unexpected error %s", err)
	}
	if c != "b01" {
		t.Errorf("want b01 but got %s", c)
	}

	s.Reset([]string{"b04", "b05", "b06"})

	c, err = s.Dispatch(jobName)
	if err != nil {
		t.Fatalf("unexpected error %s", err)
	}
	if c != "b04" {
		t.Errorf("want b02 but got %s", c)
	}
}
