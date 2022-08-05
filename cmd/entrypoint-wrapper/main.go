package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/util"
)

var (
	coreScheme   = runtime.NewScheme()
	codecFactory = serializer.NewCodecFactory(coreScheme)

	encoder runtime.Encoder
)

func init() {
	utilruntime.Must(coreapi.AddToScheme(coreScheme))
	encoder = codecFactory.LegacyCodec(coreapi.SchemeGroupVersion)
}

func main() {
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opt := bindOptions(flagSet)
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("failed to parse flag set")
	}
	opt.cmd = flagSet.Args()
	if err := opt.complete(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := opt.run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type options struct {
	dry            bool
	name           string
	srcPath        string
	dstPath        string
	waitPath       string
	waitTimeoutStr string
	waitTimeout    time.Duration
	cmd            []string
	client         coreclientset.SecretInterface
	roKubeconfig   bool
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}
	flag.BoolVar(&opt.dry, "dry-run", false, "Print the secret instead of creating it")
	flag.StringVar(&opt.waitPath, "wait-for-file", "", "Wait for a file to appear at this path before starting the program")
	flag.StringVar(&opt.waitTimeoutStr, "wait-timeout", "", "Used with --wait-for-file, maximum wait time before starting the program")
	flag.BoolVar(&opt.roKubeconfig, "ro-kubeconfig", false, "If set do not copy kubeconfig to a writable location")
	return opt
}

func (o *options) complete() error {
	if len(o.cmd) == 0 {
		return fmt.Errorf("a command is required")
	}
	if w := o.waitTimeoutStr; w != "" {
		if o.waitPath == "" {
			return fmt.Errorf("--wait-timeout requires --wait-for-file")
		}
		if d, err := time.ParseDuration(w); err != nil {
			return fmt.Errorf("invalid wait timeout duration %q: %w", w, err)
		} else {
			o.waitTimeout = d
		}
	}
	if o.srcPath = os.Getenv("SHARED_DIR"); o.srcPath == "" {
		return fmt.Errorf("environment variable SHARED_DIR is empty")
	}
	o.dstPath = filepath.Join(os.TempDir(), "secret")
	os.Setenv("SHARED_DIR", o.dstPath)
	var ns string
	if ns = os.Getenv("NAMESPACE"); ns == "" {
		return fmt.Errorf("environment variable NAMESPACE is empty")
	}
	if o.name = os.Getenv("JOB_NAME_SAFE"); o.name == "" {
		return fmt.Errorf("environment variable JOB_NAME_SAFE is empty")
	}
	if !o.dry {
		var err error
		if o.client, err = loadClient(ns); err != nil {
			return err
		}
	}
	return nil
}

func (o *options) run() error {
	if err := copyDir(o.dstPath, o.srcPath); err != nil {
		return fmt.Errorf("failed to copy secret mount: %w", err)
	}
	if o.waitPath != "" {
		if err := waitForFile(o.waitPath, o.waitTimeout); err != nil {
			return fmt.Errorf("failed to wait for file: %w", err)
		}
	}
	var errs []error
	ctx, cancel := context.WithCancel(context.Background())
	go uploadKubeconfig(ctx, o.client, o.name, o.dstPath, o.dry)
	if err := execCmd(o.cmd, o.roKubeconfig); err != nil {
		errs = append(errs, fmt.Errorf("failed to execute wrapped command: %w", err))
	}
	// we will upload the secret from the post-execution state, so we know
	// that the best-effort upload of the kubeconfig can exit now and so as
	// not to race with the post-execution one
	cancel()
	if err := createSecret(o.client, o.name, o.dstPath, o.dry); err != nil {
		errs = append(errs, fmt.Errorf("failed to create/update secret: %w", err))
	}
	return utilerrors.NewAggregate(errs)
}

func loadClient(namespace string) (coreclientset.SecretInterface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster config: %w", err)
	}
	client, err := coreclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	return client.Secrets(namespace), nil
}

func copyDir(dst, src string) error {
	if err := os.MkdirAll(dst, 0770); err != nil {
		return err
	}
	dir, err := os.Open(src)
	if err != nil {
		return err
	}
	files, err := dir.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, f := range files {
		srcPath := filepath.Join(src, f)
		if stat, err := os.Stat(srcPath); err != nil {
			return err
		} else if stat.IsDir() {
			continue
		}
		srcFD, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer srcFD.Close()
		dstFD, err := os.Create(filepath.Join(dst, f))
		if err != nil {
			return err
		}
		defer dstFD.Close()
		_, err = io.Copy(dstFD, srcFD)
		if err != nil {
			return err
		}
	}
	return nil
}

func waitForFile(path string, timeout time.Duration) error {
	dir := filepath.Dir(path)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file system watcher: %w", err)
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			log.Printf("failed to close file system watcher: %v", err)
		}
	}()
	if err := watcher.Add(dir); err != nil {
		return fmt.Errorf("failed to watch file %q: %w", dir, err)
	}
	if err := syscall.Access(path, syscall.F_OK); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	for {
		select {
		case e := <-watcher.Events:
			if e.Op&fsnotify.Create != 0 && e.Name == path {
				return nil
			}
		case err := <-watcher.Errors:
			return err
		case <-time.After(timeout):
			log.Printf("warning: timeout after waiting %s for file %q", timeout, path)
			return nil
		}
	}
}

func execCmd(argv []string, skipManageKubeconfig bool) error {
	proc := exec.Command(argv[0], argv[1:]...)
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	if proc.Env == nil {
		// the command inherits the environment if it's nil,
		// explicitly set it so when we change it, we add to
		// the inherited set instead of overwriting
		proc.Env = os.Environ()
	}
	manageHome(proc)
	manageCLI(proc)

	if !skipManageKubeconfig {
		if err := manageKubeconfig(proc); err != nil {
			return err
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	if err := proc.Start(); err != nil {
		return fmt.Errorf("failed to start main process: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case s := <-sig:
				fmt.Fprintf(os.Stderr, "received signal %d, forwarding\n", s)
				if err := proc.Process.Signal(s); err != nil {
					logrus.WithError(err).Error("Failed to forward signal")
				}
			}
		}
	}()
	return proc.Wait()
}

// manageCLI configures the PATH to include a CLI_DIR if one was provided
func manageCLI(proc *exec.Cmd) {
	cliDir, set := os.LookupEnv(api.CliEnv)
	if set {
		proc.Env = append(proc.Env, fmt.Sprintf("PATH=%s:%s", os.Getenv("PATH"), cliDir))
	}
}

// manageHome provides a writeable home so kubectl discovery can be cached
func manageHome(proc *exec.Cmd) {
	home, set := os.LookupEnv("HOME")
	needHome := !set
	if set {
		if err := syscall.Access(home, syscall.O_RDWR); err != nil {
			// $HOME is set but not writeable
			needHome = true
		}
	}
	if needHome {
		// the last of any duplicate keys is used for env
		proc.Env = append(proc.Env, "HOME=/alabama")
	}
}

// manageKubeconfig provides a unique writeable kubeconfig so users can for example set a namespace,
// but changes are not propagated to subsequent steps to limit the amount of possible mistakes
func manageKubeconfig(proc *exec.Cmd) error {
	if original, set := os.LookupEnv("KUBECONFIG"); set {
		writableCopy, err := ioutil.TempFile("", "kubeconfig-*")
		if err != nil {
			return fmt.Errorf("could not create unique, writeable $KUBECONFIG copy: %w", err)
		}
		proc.Env = append(proc.Env, fmt.Sprintf("KUBECONFIG=%s", writableCopy.Name()))
		// the source KUBECONFIG may begin to exist if it does not exist at the start, so poll for it
		go func() {
			if err := wait.PollImmediateInfinite(time.Second, func() (done bool, err error) {
				if _, err := os.Stat(original); err != nil {
					if os.IsNotExist(err) {
						return false, nil
					}
					return false, err
				}
				source, err := os.Open(original)
				if err != nil {
					return true, err
				}
				if _, err := io.Copy(writableCopy, source); err != nil {
					return true, err
				}
				if err := writableCopy.Close(); err != nil {
					return true, err
				}
				if err := source.Close(); err != nil {
					return true, err
				}
				return true, nil
			}); err != nil {
				logrus.WithError(err).Warn("could not populate unique, writeable $KUBECONFIG copy: %w", err)
			}
		}()
	}
	return nil
}

func createSecret(client coreclientset.SecretInterface, name, dir string, dry bool) error {
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat directory %q: %w", dir, err)
	}
	secret, err := util.SecretFromDir(dir)
	if err != nil {
		return fmt.Errorf("failed to generate secret: %w", err)
	}
	secret.Name = name
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels[api.SkipCensoringLabel] = "true"
	if dry {
		err := encoder.Encode(secret, os.Stdout)
		if err != nil {
			return fmt.Errorf("failed to log secret: %w", err)
		}
	} else if _, err := client.Update(context.TODO(), secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}
	return nil
}

// uploadKubeconfig will do a best-effort attempt at uploading a kubeconfig
// file if one does not exist at the time we start running but one does get
// created while executing the command
func uploadKubeconfig(ctx context.Context, client coreclientset.SecretInterface, name, dir string, dry bool) {
	if _, err := os.Stat(path.Join(dir, "kubeconfig")); err == nil {
		// kubeconfig already exists, no need to do anything
		return
	}
	var uploadErr error
	if err := wait.PollUntil(time.Second, func() (done bool, err error) {
		if _, uploadErr = os.Stat(path.Join(dir, "kubeconfig")); uploadErr != nil {
			return false, nil
		}
		// kubeconfig exists, we can upload it
		uploadErr = createSecret(client, name, dir, dry)
		return uploadErr == nil, nil // retry errors
	}, ctx.Done()); !errors.Is(err, wait.ErrWaitTimeout) {
		log.Printf("Failed to upload $KUBECONFIG: %v: %v\n", err, uploadErr)
	}
}
