module github.com/openshift/ci-tools

go 1.13

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v12.2.0+incompatible
	github.com/Sirupsen/logrus => github.com/sirupsen/logrus v1.6.0
	github.com/containerd/containerd => github.com/containerd/containerd v1.3.0

	github.com/docker/docker => github.com/openshift/moby-moby v1.4.2-0.20190308215630-da810a85109d
	github.com/moby/buildkit => github.com/dmcgowan/buildkit v0.0.0-20170731200553-da2b9dc7dab9
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20200116152001-92a2713fa240
	github.com/openshift/library-go => github.com/openshift/library-go v0.0.0-20200316194709-c2d07ed650c4
	k8s.io/api => k8s.io/api v0.17.3
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.17.3
	k8s.io/apimachinery => k8s.io/apimachinery v0.17.3
	k8s.io/client-go => k8s.io/client-go v0.17.3
	k8s.io/code-generator => k8s.io/code-generator v0.17.3
	k8s.io/component-base => k8s.io/component-base v0.0.0-20191122220729-2684fb322cb9
	k8s.io/kubectl => k8s.io/kubectl v0.0.0-20191122225023-1e3c8b70f494
)

require (
	github.com/GoogleCloudPlatform/testgrid v0.0.13
	github.com/alecthomas/chroma v0.7.1
	github.com/blang/semver v3.5.1+incompatible
	github.com/docker/distribution v2.7.1+incompatible
	github.com/docker/docker v1.13.1 // indirect
	github.com/docker/spdystream v0.0.0-20181023171402-6480d4af844c // indirect
	github.com/fsnotify/fsnotify v1.4.9 // indirect
	github.com/getlantern/deepcopy v0.0.0-20160317154340-7f45deb8130a
	github.com/ghodss/yaml v1.0.0
	github.com/google/go-cmp v0.5.0
	github.com/google/gofuzz v1.1.0
	github.com/kataras/tablewriter v0.0.0-20180708051242-e063d29b7c23
	github.com/mattn/go-zglob v0.0.2
	github.com/montanaflynn/stats v0.6.3
	github.com/openshift/api v0.0.0-20200326160804-ecb9283fe820
	github.com/openshift/builder v0.0.0-20200325182657-6a52122d21e0
	github.com/openshift/client-go v0.0.0-20200116152001-92a2713fa240
	github.com/openshift/imagebuilder v1.1.7-0.20200721215719-ac4334598b95
	github.com/openshift/library-go v0.0.0-20190904120025-7d4acc018c61
	github.com/openshift/openshift-apiserver v0.0.0-alpha.0
	github.com/pkg/errors v0.9.1
	github.com/pmezard/go-difflib v1.0.0
	github.com/polyfloyd/go-errorlint v0.0.0-20200429095719-920be198a950
	github.com/prometheus/client_golang v1.5.0
	github.com/prometheus/common v0.9.1
	github.com/shurcooL/githubv4 v0.0.0-20191102174205-af46314aec7b
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/afero v1.2.2
	golang.org/x/oauth2 v0.0.0-20200107190931-bf48bf16ab8d
	golang.org/x/sync v0.0.0-20200625203802-6e8e738ad208
	gopkg.in/fsnotify.v1 v1.4.7
	k8s.io/api v0.18.2
	k8s.io/apimachinery v0.18.2
	k8s.io/client-go v9.0.0+incompatible
	k8s.io/test-infra v0.0.0-20200725055416-2388ca759f42
	k8s.io/utils v0.0.0-20200324210504-a9aa75ae1b89
	sigs.k8s.io/boskos v0.0.0-20200530174753-71e795271860
	sigs.k8s.io/controller-runtime v0.5.4
	sigs.k8s.io/controller-tools v0.3.0
	sigs.k8s.io/yaml v1.2.0
)
