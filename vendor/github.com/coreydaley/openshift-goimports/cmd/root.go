/*
Copyright © 2020 Corey Daley <cdaley@redhat.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"golang.org/x/mod/modfile"

	klog "k8s.io/klog/v2"

	"github.com/coreydaley/openshift-goimports/pkg/imports"
	"github.com/coreydaley/openshift-goimports/pkg/util"
)

var (
	module  string
	path    string
	dry     bool
	cfgFile string
	wg      sync.WaitGroup
	impLine = regexp.MustCompile(`^\s+(?:[\w\.]+\s+)?"(.+)"`)
	vendor  = regexp.MustCompile(`vendor/`)
	files   = make(chan string, 1000)
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "openshift-goimports",
	Short: "Organize go imports according to OpenShift best practices.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		// If no module is provided, let's try to determine it programatically
		if len(module) == 0 {
			modFilePath := fmt.Sprintf("%s/go.mod", path)
			klog.V(2).Infof("No module path provided, checking for %q", modFilePath)
			if _, err := os.Stat(modFilePath); os.IsNotExist(err) {
				klog.Error("no go.mod file found and no module name provided.")
				os.Exit(1)
			}
			f, err := ioutil.ReadFile(fmt.Sprintf("%s/go.mod", path))
			if err != nil {
				klog.Errorf("unable to open go.mod file for reading: %v", err)
				os.Exit(1)
			}
			module = modfile.ModulePath(f)
			if len(module) == 0 {
				klog.Error("unable to automatically determine module path, please provide one using the --module flag")
				os.Exit(1)
			}
		}

		klog.V(2).Infof("Using module path %q", module)

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go imports.Format(files, &wg, &module, &dry)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := filepath.Walk(path,
				func(path string, f os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					if f.IsDir() && f.Name() == "vendor" {
						return filepath.SkipDir
					}
					if util.IsGoFile(f) {
						klog.V(2).Infof("Queueing %s", path)
						files <- path
					}
					return nil
				})
			if err != nil {
				klog.Error(err)
			}
			close(files)
		}()

		wg.Wait()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		klog.Error(err)
		os.Exit(1)
	}
}

func init() {
	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	cobra.OnInitialize()

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.openshift-goimports.yaml)")

	rootCmd.Flags().StringVarP(&path, "path", "p", ".", "The path to the go module to organize. Defaults to the current directory.")
	rootCmd.Flags().StringVarP(&module, "module", "m", "", "The name of the go module. Example: github.com/example-org/example-repo")
	rootCmd.Flags().BoolVarP(&dry, "dry", "d", false, "Dry run only, do not actually make any changes to files")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			klog.Error(err)
			os.Exit(1)
		}

		// Search config in home directory with name ".openshift-goimports" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigName(".openshift-goimports")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		klog.Infof("Using config file: %s", viper.ConfigFileUsed())
	}
}
