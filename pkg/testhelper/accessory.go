package testhelper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

// ArtifactDir determines where artifacts should for for a test case.
func ArtifactDir(t *T) string {
	var baseDir string
	if dir, set := os.LookupEnv("ARTIFACT_DIR"); set {
		baseDir = dir
	} else {
		baseDir = t.TempDir()
	}
	artifactDir := filepath.Join(baseDir, strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(t.Name()))
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		t.Fatalf("could not create artifact dir for ci-operator: %v", err)
	}
	t.Logf("Saving artifacts to %s.", artifactDir)
	return artifactDir
}

func NewT(ctx context.Context, t *testing.T) *T {
	return &T{
		T:      t,
		ctx:    ctx,
		errors: make(chan error, 10),
		fatals: make(chan error, 10),
	}
}

// T allows us to provide a similar UX to the testing.T while
// doing so in a multi-threaded context. The Go unit testing
// framework only allows the top-level goroutine to call FailNow
// so it's important to provide this interface to all the routines
// that want to be able to control the test execution flow.
type T struct {
	*testing.T
	ctx context.Context

	errors chan error
	fatals chan error
}

// the testing.T logger is not threadsafe...
func (t *T) Log(args ...interface{}) {
	t.T.Log(args...)
}

// the testing.T logger is not threadsafe...
func (t *T) Logf(format string, args ...interface{}) {
	t.T.Logf(format, args...)
}

func (t *T) Errorf(format string, args ...interface{}) {
	t.errors <- fmt.Errorf(format, args...)
}

func (t *T) Fatalf(format string, args ...interface{}) {
	t.fatals <- fmt.Errorf(format, args...)
}

func (t *T) Fatal(args ...interface{}) {
	t.fatals <- fmt.Errorf(fmt.Sprintln(args...))
}

// Wait receives data from producer threads and forwards it
// to the delegate; this call is blocking.
func (t *T) Wait() {
	t.T.Helper()
	for {
		select {
		case <-t.ctx.Done():
			return
		case err := <-t.errors:
			t.T.Error(err)
		case fatal := <-t.fatals:
			t.T.Fatal(fatal)
		}
	}
}

type PortFlags func(port string) []string

func NewAccessory(command string, args []string, portFlags PortFlags) *Accessory {
	return &Accessory{
		command: command,
		args:    args,
		flags:   portFlags,
	}
}

type Accessory struct {
	command    string
	args       []string
	port       string
	healthPort string

	flags PortFlags
}

// Run begins the accessory process. This call is not blocking.
func (a *Accessory) Run(t *T, parentCtx context.Context) {
	a.port, a.healthPort = getFreePort(t), getFreePort(t)
	ctx, cancel := context.WithCancel(parentCtx)
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		t.Logf("cleanup: killing `%s` process", a.command)
		cancel()
		<-cleanupCtx.Done()
	})
	cmd := exec.CommandContext(ctx, a.command, append(a.args, flags(map[string]string{
		"port":        a.port,
		"health-port": a.healthPort,
	})...)...)
	t.Logf("running: %v", cmd.Args)
	artifactDir := ArtifactDir(t)
	logFile, err := os.Create(filepath.Join(artifactDir, fmt.Sprintf("%s.log", a.command)))
	if err != nil {
		t.Fatalf("could not create log file: %v", err)
	}
	log := bytes.Buffer{}
	tee := io.TeeReader(&log, logFile)
	cmd.Stdout = &log
	cmd.Stderr = &log
	go func() {
		defer func() { cleanupCancel() }()
		err := cmd.Run()
		data, readErr := ioutil.ReadAll(tee)
		if readErr != nil {
			t.Logf("could not read `%s` log: %v", a.command, readErr)
		}
		t.Logf("`%s` logs:\n%v", a.command, string(data))
		if err != nil && ctx.Err() == nil {
			// we care about errors in the process that did not result from the
			// context expiring and us killing it
			t.Fatalf("`%s` failed: %v logs:\n%v", a.command, err, string(data))
		}
	}()
}

// Ready returns when the accessory process is ready to serve data.
func (a *Accessory) Ready(t *T) {
	if waitErr := wait.PollImmediate(1*time.Second, 30*time.Second, func() (done bool, err error) {
		resp, getErr := http.Get(fmt.Sprintf("http://127.0.0.1:%s/healthz/ready", a.healthPort))
		defer func() {
			if resp == nil || resp.Body == nil {
				return
			}
			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Logf("could not close response body: %v", closeErr)
			}
		}()
		if resp != nil {
			t.Logf("`%s` readiness probe: %v", a.command, resp.StatusCode)
		}
		if getErr != nil {
			t.Logf("`%s` readiness probe error: %v:", a.command, getErr)
		}
		return (resp != nil && resp.StatusCode == http.StatusOK) && getErr == nil, nil
	}); waitErr != nil {
		t.Fatalf("could not wait for `%s` to be healthy: %v", a.command, waitErr)
	}
}

// Flags exposes the port on which we are serving content and
// any other flags that are needed for the ci-operator to consume
// this accessory.
func (a *Accessory) Flags() []string {
	return a.flags(a.port)
}

var ports = sync.Map{}

// getFreePort asks the kernel for a free open port that is ready to use.
func getFreePort(t *T) string {
	for {
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			t.Fatalf("could not resolve free port: %v", err)
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			t.Fatalf("could not listen on free port: %v", err)
		}
		defer func() {
			if err := l.Close(); err != nil {
				t.Errorf("could not close listener: %v", err)
			}
		}()
		port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
		if _, previouslyAllocated := ports.LoadOrStore(port, nil); !previouslyAllocated {
			// we've never seen this before, we can use it
			return port
		}
	}
}

func flags(data map[string]string) []string {
	var out []string
	for key, value := range data {
		out = append(out, flag(key, value))
	}
	return out
}

func flag(flag, value string) string {
	return fmt.Sprintf("--%s=%s", flag, value)
}
