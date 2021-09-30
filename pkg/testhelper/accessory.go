package testhelper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

// ArtifactDir determines where artifacts should for for a test case.
func ArtifactDir(t TestingTInterface) string {
	var baseDir string
	if dir, set := os.LookupEnv("ARTIFACT_DIR"); set {
		baseDir = dir
	} else {
		baseDir = t.TempDir()
	}
	artifactDir := filepath.Join(baseDir, strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(t.Name()))
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		t.Fatalf("could not create artifact dir: %v", err)
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

type PortFlags func(port, healthPort string) []string

func NewAccessory(command string, args []string, portFlags, clientPortFlags PortFlags, env ...string) *Accessory {
	return &Accessory{
		command: command,
		args:    args,
		env:     env,

		flags:       portFlags,
		clientFlags: clientPortFlags,
	}
}

type Accessory struct {
	command    string
	args       []string
	env        []string
	port       string
	healthPort string

	flags       PortFlags
	clientFlags PortFlags
}

// RunFromFrameworkRunner begins the accessory process. Only test/e2e/framework.Run
// is allowed to call this as it required additional synchronization or your
// tests might pass incorrectly.
func (a *Accessory) RunFromFrameworkRunner(t TestingTInterface, parentCtx context.Context, stream bool) {
	a.run(parentCtx, t, t.Fatalf, stream)
}

// Run begins the accessory process. this call is not blocking.
// Because testing.T does not allow to call Fatalf in a distinct
// goroutine, this will use Errorf instead.
func (a *Accessory) Run(t TestingTInterface) {
	a.run(context.Background(), t, t.Errorf, false)
}

func (a *Accessory) run(parentCtx context.Context, t TestingTInterface, failfunc func(format string, args ...interface{}), stream bool) {
	a.port, a.healthPort = GetFreePort(t), GetFreePort(t)
	ctx, cancel := context.WithCancel(parentCtx)
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		t.Logf("cleanup: killing `%s` process", a.command)
		cancel()
		<-cleanupCtx.Done()
	})
	cmd := exec.CommandContext(ctx, a.command, append(a.args, a.flags(a.port, a.healthPort)...)...)
	cmd.Env = append(cmd.Env, a.env...)
	t.Logf("running: %v %v", strings.Join(cmd.Env, " "), strings.Join(cmd.Args, " "))
	artifactDir := ArtifactDir(t)
	logFile, err := os.Create(filepath.Join(artifactDir, fmt.Sprintf("%s.log", a.command)))
	if err != nil {
		failfunc("could not create log file: %v", err)
	}
	log := bytes.Buffer{}
	writers := []io.Writer{&log, logFile}
	if stream {
		writers = append(writers, os.Stdout)
	}
	mw := io.MultiWriter(writers...)
	cmd.Stdout = mw
	cmd.Stderr = mw
	go func() {
		defer func() { cleanupCancel() }()
		err := cmd.Run()
		data := log.String()
		t.Logf("`%s` logs:\n%v", a.command, data)
		if err != nil && ctx.Err() == nil {
			// we care about errors in the process that did not result from the
			// context expiring and us killing it
			failfunc("`%s` failed: %v logs:\n%v", a.command, err, data)
		}
	}()

}

type ReadyOptions struct {
	// ReadyURL is the url to GET to check for readyness. Defaults to
	// http://127.0.0.1:${HEALTH_PORT}/healthz/ready
	ReadyURL string
	WaitFor  int64
}

type ReadyOption func(*ReadyOptions)

// Ready returns when the accessory process is ready to serve data.
func (a *Accessory) Ready(t TestingTInterface, o ...ReadyOption) {
	opts := ReadyOptions{
		ReadyURL: fmt.Sprintf("http://127.0.0.1:%s/healthz/ready", a.healthPort),
		WaitFor:  90,
	}
	for _, o := range o {
		o(&opts)
	}
	WaitForHTTP200(opts.ReadyURL, a.command, opts.WaitFor, t)
}

var _ TestingTInterface = &testing.T{}
var _ TestingTInterface = &T{}

// TestingTInterface contains the methods that are implemented by both our T and testing.T
type TestingTInterface interface {
	Cleanup(f func())
	Deadline() (deadline time.Time, ok bool)
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Fail()
	FailNow()
	Failed() bool
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
	Helper()
	Log(args ...interface{})
	Logf(format string, args ...interface{})
	Name() string
	Parallel()
	Skip(args ...interface{})
	SkipNow()
	Skipf(format string, args ...interface{})
	Skipped() bool
	TempDir() string
}

// WaitForHTTP200 waits waitFor seconds for the provided addr to return a http/200. If that doesn't
// happen, it will call t.Fatalf
func WaitForHTTP200(addr, command string, waitFor int64, t TestingTInterface) {
	if waitErr := wait.PollImmediate(1*time.Second, time.Duration(waitFor)*time.Second, func() (done bool, err error) {
		resp, getErr := http.Get(addr)
		defer func() {
			if resp == nil || resp.Body == nil {
				return
			}
			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Logf("could not close response body: %v", closeErr)
			}
		}()
		if resp != nil {
			t.Logf("`%s` readiness probe: %v", command, resp.StatusCode)
		}
		if getErr != nil {
			t.Logf("`%s` readiness probe error: %v:", command, getErr)
		}
		return (resp != nil && resp.StatusCode == http.StatusOK) && getErr == nil, nil
	}); waitErr != nil {
		t.Fatalf("could not wait for `%s` to be healthy: %v", command, waitErr)
	}
}

// ClientFlags exposes the port on which we are serving content and
// any other flags that are needed for clients to consume
// this accessory.
func (a *Accessory) ClientFlags() []string {
	if a.clientFlags == nil {
		return nil
	}
	return a.clientFlags(a.port, a.healthPort)
}

// GetFreePort asks the kernel for a free open port that is ready to use.
func GetFreePort(t TestingTInterface) string {
	for {
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			t.Fatalf("could not resolve free port: %v", err)
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			t.Fatalf("could not listen on free port: %v", err)
		}
		defer func(c io.Closer) {
			if err := c.Close(); err != nil {
				t.Errorf("could not close listener: %v", err)
			}
		}(l)
		port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
		// Tests run in -parallel will run in separate processes, so we must use the file-system
		// for sharing state and locking across them to coordinate who gets which port. Without
		// some mechanism for sharing state, the following race is possible:
		// - process A calls net.ListenTCP() to resolve a new port
		// - process A calls l.Close() to close the listener to allow accessory to use it
		// - process B calls net.ListenTCP() and resolves the same port
		// - process A attempts to use the port, fails as it is in use
		// Therefore, holding the listener open is our kernel-based lock for this process, and while
		// we hold it open we must record our intent to disk.
		lockDir := filepath.Join(os.TempDir(), "ci-tools-e2e-ports")
		if err := os.MkdirAll(lockDir, 0777); err != nil {
			t.Fatalf("could not create port lockfile dir: %v", err)
		}
		lockFile := filepath.Join(lockDir, port)
		if _, err := os.Stat(lockFile); os.IsNotExist(err) {
			// we've never seen this before, we can use it
			f, err := os.Create(lockFile)
			if err != nil {
				t.Fatalf("could not record port lockfile: %v", err)
			}
			if err := f.Close(); err != nil {
				t.Errorf("could not close port lockfile: %v", err)
			}
			// the lifecycle of an accessory (and thereby its ports) is the test lifecycle
			t.Cleanup(func() {
				if err := os.Remove(lockFile); err != nil {
					t.Errorf("failed to remove port lockfile: %v", err)
				}
			})
			t.Logf("found a never-before-seen port, returning: %s", port)
			return port
		} else if err != nil {
			t.Fatalf("could not access port lockfile: %v", err)
		}
		t.Logf("found a previously-seen port, retrying: %s", port)
	}
}
