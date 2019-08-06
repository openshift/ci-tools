module github.com/openshift/ci-tools

go 1.12

replace (
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190313205120-d7deff9243b1
	k8s.io/client-go => k8s.io/client-go v11.0.0+incompatible

)

require (
	cloud.google.com/go v0.43.0
	github.com/SierraSoftworks/sentry-go v1.1.1
	github.com/docker/spdystream v0.0.0-20181023171402-6480d4af844c // indirect
	github.com/getlantern/deepcopy v0.0.0-20160317154340-7f45deb8130a
	github.com/ghodss/yaml v1.0.0
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/mattn/go-zglob v0.0.1
	github.com/onsi/ginkgo v1.7.0 // indirect
	github.com/onsi/gomega v1.4.3 // indirect
	github.com/openshift/api v3.9.1-0.20190725193935-b7d4eb0fa1e0+incompatible
	github.com/openshift/client-go v0.0.0-20190721020503-a85ea6a6b3a5
	github.com/shurcooL/githubv4 v0.0.0-20190718010115-4ba037080260
	github.com/sirupsen/logrus v1.4.2
	golang.org/x/oauth2 v0.0.0-20190604053449-0f29369cfe45
	golang.org/x/sync v0.0.0-20190423024810-112230192c58
	google.golang.org/api v0.7.0
	k8s.io/api v0.0.0-20190725062911-6607c48751ae
	k8s.io/apimachinery v0.0.0-20190719140911-bfcf53abc9f8
	k8s.io/client-go v11.0.1-0.20190409021438-1a26190bd76a+incompatible
	k8s.io/test-infra v0.0.0-20190806224052-84f7d47e97f3
)
