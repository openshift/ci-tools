package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"reflect"

	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/openshift/ci-tools/pkg/api"
)

// installerConfigBase is the internal representation of a very basic config
var installerConfigBase = api.ReleaseBuildConfiguration{
	InputConfiguration: api.InputConfiguration{
		BaseImages: map[string]api.ImageStreamTagReference{
			"base": {
				Namespace: "ocp",
				Name:      "4.2",
				Tag:       "base",
			},
		},
		BuildRootImage: &api.BuildRootImageConfiguration{
			ImageStreamTagReference: &api.ImageStreamTagReference{
				Cluster:   "https://api.ci.openshift.org",
				Namespace: "openshift",
				Name:      "release",
				Tag:       "golang-1.10",
			},
		},
	},
	Resources: api.ResourceConfiguration{
		"*": api.ResourceRequirements{
			Limits: api.ResourceList{
				"memory": "4Gi",
			},
			Requests: api.ResourceList{
				"cpu":    "100m",
				"memory": "200Mi",
			},
		},
	},
	Tests: []api.TestStepConfiguration{
		{
			As:       "unit",
			Commands: "go test ./pkg/...",
			ContainerTestConfiguration: &api.ContainerTestConfiguration{
				From: "src",
			},
		},
		{
			As:       "e2e-aws",
			Commands: "TEST_SUITE=openshift/conformance/parallel run-tests",
			OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
				ClusterTestConfiguration: api.ClusterTestConfiguration{
					ClusterProfile: api.ClusterProfileAWS,
				},
			},
		},
		{
			As: "e2e-multistage",
			MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
				ClusterProfile: api.ClusterProfileAWS,
				Pre: []api.TestStep{
					{
						LiteralTestStep: &api.LiteralTestStep{
							As:       "ipi-install-rbac",
							From:     "installer",
							Commands: "setup-rbac\n",
							Resources: api.ResourceRequirements{
								Requests: api.ResourceList{"cpu": "1000m", "mem": "2Gi"},
							}},
					}, {
						LiteralTestStep: &api.LiteralTestStep{
							As:       "ipi-install-install",
							From:     "installer",
							Commands: "openshift-cluster install\n",
							Resources: api.ResourceRequirements{
								Requests: api.ResourceList{"cpu": "1000m", "mem": "2Gi"},
							}},
					}},
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       "test",
						From:     "src",
						Commands: "make custom-e2e",
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "100m", "memory": "200M"},
						}},
				}},
				Post: []api.TestStep{
					{
						LiteralTestStep: &api.LiteralTestStep{
							As:       "ipi-deprovision-must-gather",
							From:     "installer",
							Commands: "gather\n",
							Resources: api.ResourceRequirements{
								Requests: api.ResourceList{"cpu": "1000m", "mem": "2Gi"},
							}},
					},
					{
						LiteralTestStep: &api.LiteralTestStep{
							As:       "ipi-deprovision-deprovision",
							From:     "installer",
							Commands: "openshift-cluster destroy\n",
							Resources: api.ResourceRequirements{
								Requests: api.ResourceList{"cpu": "1000m", "mem": "2Gi"},
							}},
					},
				},
			},
		},
	},
}

func main() {
	// these flags will be used for future tests that modify registry files to ensure that the resolver picks up changes
	//registryPath := flag.String("registry", "", "Path to step registry")
	//configPath := flag.String("config", "", "Path to step config dir")
	serverAddress := flag.String("serverAddress", "", "HTTP address to config resolver")
	flag.Parse()
	config1, err := http.Get(fmt.Sprintf("%s/config?org=openshift&repo=installer&branch=release-4.2", *serverAddress))
	if err != nil {
		log.Fatalf("Failed to get config: %v", err)
	}
	defer config1.Body.Close()
	body, err := ioutil.ReadAll(config1.Body)
	if err != nil {
		log.Fatalf("Failed to read body: %v", err)
	}
	var config api.ReleaseBuildConfiguration
	err = json.Unmarshal(body, &config)
	if err != nil {
		log.Fatalf("Failed to unmarshal: %v", err)
	}
	if !reflect.DeepEqual(installerConfigBase, config) {
		log.Fatalf("Got incorrect output: %s", diff.ObjectReflectDiff(installerConfigBase, config))
	}
}
