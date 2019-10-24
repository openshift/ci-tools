module github.com/openshift/ci-tools

go 1.12

replace (
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190817020851-f2f3a405f61d
	k8s.io/client-go => k8s.io/client-go v0.0.0-20190918200256-06eb1244587a
)

require (
	cloud.google.com/go v0.44.3
	github.com/GoogleCloudPlatform/testgrid v0.0.0-20191016232453-9f0319fc1197
	github.com/SierraSoftworks/sentry-go v1.1.1
	github.com/docker/spdystream v0.0.0-20181023171402-6480d4af844c // indirect
	github.com/elazarl/goproxy v0.0.0-20190711103511-473e67f1d7d2 // indirect
	github.com/elazarl/goproxy/ext v0.0.0-20190711103511-473e67f1d7d2 // indirect
	github.com/fsnotify/fsnotify v1.4.7
	github.com/getlantern/deepcopy v0.0.0-20160317154340-7f45deb8130a
	github.com/ghodss/yaml v1.0.0
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/google/go-cmp v0.3.1
	github.com/mattn/go-zglob v0.0.1
	github.com/openshift/api v3.9.1-0.20190725193935-b7d4eb0fa1e0+incompatible
	github.com/openshift/client-go v0.0.0-20190721020503-a85ea6a6b3a5
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v1.0.0
	github.com/shurcooL/githubv4 v0.0.0-20190718010115-4ba037080260
	github.com/sirupsen/logrus v1.4.2
	golang.org/x/oauth2 v0.0.0-20190604053449-0f29369cfe45
	golang.org/x/sync v0.0.0-20190423024810-112230192c58
	google.golang.org/api v0.9.0
	k8s.io/api v0.0.0-20190918195907-bd6ac527cfd2
	k8s.io/apimachinery v0.0.0-20190817020851-f2f3a405f61d
	k8s.io/client-go v11.0.1-0.20190805182717-6502b5e7b1b5+incompatible
	k8s.io/test-infra v0.0.0-20191024014506-4a8918207c89
	sigs.k8s.io/yaml v1.1.0
)
