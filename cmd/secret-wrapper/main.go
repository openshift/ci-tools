package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/ci-tools/pkg/util"
)

const (
	dir = "/tmp/secret"
)

func main() {
	if len(os.Args) == 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s cmd...\n", os.Args[0])
		os.Exit(1)
	}
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(argv []string) error {
	var ns, name string
	if ns = os.Getenv("NAMESPACE"); ns == "" {
		return fmt.Errorf("environment variable NAMESPACE is empty")
	}
	if name = os.Getenv("JOB_NAME_SAFE"); name == "" {
		return fmt.Errorf("environment variable JOB_NAME_SAFE is empty")
	}
	client, err := loadClient(ns)
	if err != nil {
		return err
	}
	if err := execCmd(os.Args); err != nil {
		return fmt.Errorf("failed to execute wrapped command: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat directory %q: %v", dir, err)
	}
	secret, err := util.SecretFromDir(dir)
	if err != nil {
		return fmt.Errorf("failed to generate secret: %v", err)
	}
	secret.Name = name
	if _, err := util.UpdateSecret(client, secret); err != nil {
		return fmt.Errorf("failed to update secret: %v", err)
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
	proc := exec.Command(argv[1], argv[2:]...)
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
