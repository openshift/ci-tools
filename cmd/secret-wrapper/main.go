package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/util"
)

func main() {
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opt := bindOptions(flagSet)
	flagSet.Parse(os.Args[1:])
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
	dry       bool
	dir, name string
	cmd       []string
	client    coreclientset.SecretInterface
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
	var ns string
	if ns = os.Getenv("NAMESPACE"); ns == "" {
		return fmt.Errorf("environment variable NAMESPACE is empty")
	}
	if o.name = os.Getenv("JOB_NAME_SAFE"); o.name == "" {
		return fmt.Errorf("environment variable JOB_NAME_SAFE is empty")
	}
	o.dir = filepath.Join(os.TempDir(), "secret")
	if !o.dry {
		var err error
		if o.client, err = loadClient(ns); err != nil {
			return err
		}
	}
	return nil
}

func (o *options) run() error {
	if err := execCmd(o.cmd); err != nil {
		return fmt.Errorf("failed to execute wrapped command: %v", err)
	}
	if _, err := os.Stat(o.dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat directory %q: %v", o.dir, err)
	}
	secret, err := util.SecretFromDir(o.dir)
	if err != nil {
		return fmt.Errorf("failed to generate secret: %v", err)
	}
	secret.Name = o.name
	if o.dry {
		logger := steps.DryLogger{}
		logger.AddObject(secret)
		if err := logger.Log(); err != nil {
			return fmt.Errorf("failed to log secret: %v", err)
		}
	} else {
		if _, err := o.client.Update(secret); err != nil {
			return fmt.Errorf("failed to update secret: %v", err)
		}
	}
	return nil
}

func loadClient(namespace string) (coreclientset.SecretInterface, error) {
	config, err := util.LoadClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster config: %v", err)
	}
	client, err := coreclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %v", err)
	}
	return client.Secrets(namespace), nil
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
				proc.Process.Signal(s)
			}
		}
	}()
	return proc.Run()
}
