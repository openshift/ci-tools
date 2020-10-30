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
	"os"
	"path/filepath"
	"regexp"
	"sync"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	klog "k8s.io/klog/v2"

	"github.com/coreydaley-redhat/osgoimports/pkg/imports"
	"github.com/coreydaley-redhat/osgoimports/pkg/util"
)

var (
	module  string
	path    string
	cfgFile string
	wg      sync.WaitGroup
	impLine = regexp.MustCompile(`^\s+(?:[\w\.]+\s+)?"(.+)"`)
	vendor  = regexp.MustCompile(`vendor/`)
	files   = make(chan string, 10)
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "osgoimports",
	Short: "Organize go imports according to OpenShift best practices.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go imports.Format(files, &wg, &module)
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

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.osgoimports.yaml)")

	rootCmd.Flags().StringVarP(&path, "path", "p", ".", "The path to the go module to organize. Defaults to the current directory.")
	rootCmd.Flags().StringVarP(&module, "module", "m", "", "The name of the go module. Example: github.com/coreydaley-redhat/osgoimports")
	rootCmd.MarkFlagRequired("module")
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

		// Search config in home directory with name ".osgoimports" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigName(".osgoimports")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		klog.Infof("Using config file: %s", viper.ConfigFileUsed())
	}
}
