module github.com/openshift/ci-tools

go 1.13

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v12.2.0+incompatible
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20200116152001-92a2713fa240
	github.com/openshift/library-go => github.com/openshift/library-go v0.0.0-20200316194709-c2d07ed650c4
	k8s.io/api => k8s.io/api v0.17.3
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.17.3
	k8s.io/apimachinery => k8s.io/apimachinery v0.17.3
	k8s.io/client-go => k8s.io/client-go v0.17.3
)

require (
	github.com/GoogleCloudPlatform/testgrid v0.0.7
	github.com/alecthomas/chroma v0.7.1
	github.com/docker/distribution v2.7.1+incompatible
	github.com/docker/spdystream v0.0.0-20181023171402-6480d4af844c // indirect
	github.com/elazarl/goproxy v0.0.0-20190711103511-473e67f1d7d2 // indirect
	github.com/elazarl/goproxy/ext v0.0.0-20190711103511-473e67f1d7d2 // indirect
	github.com/fsnotify/fsnotify v1.4.9 // indirect
	github.com/getlantern/deepcopy v0.0.0-20160317154340-7f45deb8130a
	github.com/ghodss/yaml v1.0.0
	github.com/golang/protobuf v1.3.5 // indirect
	github.com/google/go-cmp v0.4.0
	github.com/google/gofuzz v1.1.0
	github.com/mattn/go-zglob v0.0.1
	github.com/openshift/api v0.0.0-20200326160804-ecb9283fe820
	github.com/openshift/client-go v0.0.0-20200116152001-92a2713fa240
	github.com/openshift/library-go v0.0.0-00010101000000-000000000000
	github.com/openshift/openshift-apiserver v0.0.0-alpha.0
	github.com/pkg/errors v0.9.1
	github.com/pmezard/go-difflib v1.0.0
	github.com/prometheus/client_golang v1.5.0
	github.com/shurcooL/githubv4 v0.0.0-20191102174205-af46314aec7b
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/afero v1.2.2
	golang.org/x/oauth2 v0.0.0-20200107190931-bf48bf16ab8d
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e
	gopkg.in/fsnotify.v1 v1.4.7
	k8s.io/api v0.18.0
	k8s.io/apimachinery v0.18.0
	k8s.io/client-go v9.0.0+incompatible
	k8s.io/test-infra v0.0.0-20200518220536-8da074ad41b6
	k8s.io/utils v0.0.0-20200324210504-a9aa75ae1b89
	sigs.k8s.io/controller-runtime v0.5.1
	sigs.k8s.io/yaml v1.2.0
)
