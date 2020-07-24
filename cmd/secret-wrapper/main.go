package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/sirupsen/logrus"
	coreapi "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	"github.com/openshift/ci-tools/pkg/util"
)

var (
	coreScheme   = runtime.NewScheme()
	codecFactory = serializer.NewCodecFactory(coreScheme)
	corev1Codec  = codecFactory.LegacyCodec(coreapi.SchemeGroupVersion)
	rbacv1Codec  = codecFactory.LegacyCodec(rbacv1.SchemeGroupVersion)

	encoder runtime.Encoder
	decoder runtime.Decoder
)

func init() {
	utilruntime.Must(coreapi.AddToScheme(coreScheme))
	encoder = codecFactory.LegacyCodec(coreapi.SchemeGroupVersion)
	decoder = codecFactory.UniversalDecoder(coreapi.SchemeGroupVersion)
}

func main() {
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opt := bindOptions(flagSet)
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("Failed to parsse flagset")
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
	dry     bool
	name    string
	srcPath string
	dstPath string
	cmd     []string
	client  coreclientset.SecretInterface
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}
	flag.BoolVar(&opt.dry, "dry-run", false, "Print the secret instead of creating it")
	return opt
}

func (o *options) complete() error {
	if len(o.cmd) == 0 {
		return fmt.Errorf("a command is required")
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
	var errs []error
	if err := execCmd(o.cmd); err != nil {
		errs = append(errs, fmt.Errorf("failed to execute wrapped command: %w", err))
	}
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

func execCmd(argv []string) error {
	proc := exec.Command(argv[0], argv[1:]...)
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			select {
			case <-ctx.Done():
				break
			case s := <-sig:
				fmt.Fprintf(os.Stderr, "received signal %d, forwarding\n", s)
				if err := proc.Process.Signal(s); err != nil {
					logrus.WithError(err).Error("Failed to forward signal")
				}
			}
		}
	}()
	return proc.Run()
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
	if dry {
		err := encoder.Encode(secret, os.Stdout)
		if err != nil {
			return fmt.Errorf("failed to log secret: %w", err)
		}
	} else if _, err := client.Update(secret); err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}
	return nil
}
