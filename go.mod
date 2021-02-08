module github.com/openshift/ci-tools

go 1.15

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v14.2.0+incompatible
	github.com/Sirupsen/logrus => github.com/sirupsen/logrus v1.6.0
	github.com/containerd/containerd => github.com/containerd/containerd v0.2.10-0.20180716142608-408d13de2fbb

	github.com/docker/docker => github.com/openshift/moby-moby v1.4.2-0.20190308215630-da810a85109d
	github.com/moby/buildkit => github.com/dmcgowan/buildkit v0.0.0-20170731200553-da2b9dc7dab9
	github.com/openshift/api => github.com/openshift/api v0.0.0-20201120165435-072a4cd8ca42
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20200521150516-05eb9880269c
	github.com/openshift/library-go => github.com/openshift/library-go v0.0.0-20200527213645-a9b77f5402e3
	k8s.io/client-go => k8s.io/client-go v0.20.2
	k8s.io/component-base => k8s.io/component-base v0.20.2
	k8s.io/kubectl => k8s.io/kubectl v0.20.2
)

require (
	cloud.google.com/go/storage v1.12.0
	github.com/GoogleCloudPlatform/testgrid v0.0.30
	github.com/alecthomas/chroma v0.8.2-0.20201103103104-ab61726cdb54
	github.com/andygrunwald/go-jira v1.13.0
	github.com/blang/semver v3.5.1+incompatible
	github.com/coreydaley/openshift-goimports v0.0.0-20201111145504-7b4aecddd198
	github.com/docker/distribution v2.7.1+incompatible
	github.com/docker/spdystream v0.0.0-20181023171402-6480d4af844c // indirect
	github.com/getlantern/deepcopy v0.0.0-20160317154340-7f45deb8130a
	github.com/ghodss/yaml v1.0.0
	github.com/go-bindata/go-bindata/v3 v3.1.3
	github.com/google/go-cmp v0.5.2
	github.com/google/gofuzz v1.1.0
	github.com/hashicorp/go-retryablehttp v0.6.6
	github.com/hashicorp/go-version v1.2.1
	github.com/kataras/tablewriter v0.0.0-20180708051242-e063d29b7c23
	github.com/mattn/go-zglob v0.0.2
	github.com/montanaflynn/stats v0.6.3
	github.com/openshift/api v0.0.0-20200521101457-60c476765272
	github.com/openshift/builder v0.0.0-20200325182657-6a52122d21e0
	github.com/openshift/client-go v3.9.0+incompatible
	github.com/openshift/imagebuilder v1.1.1
	github.com/openshift/library-go v0.0.0-20200127110935-527e40ed17d9
	github.com/openshift/openshift-apiserver v0.0.0-alpha.0
	github.com/pkg/errors v0.9.1
	github.com/pmezard/go-difflib v1.0.0
	github.com/polyfloyd/go-errorlint v0.0.0-20200429095719-920be198a950
	github.com/prometheus/client_golang v1.7.1
	github.com/prometheus/common v0.10.0
	github.com/shurcooL/githubv4 v0.0.0-20191102174205-af46314aec7b
	github.com/sirupsen/logrus v1.6.0
	github.com/slack-go/slack v0.7.3
	github.com/spf13/afero v1.4.1
	golang.org/x/oauth2 v0.0.0-20200902213428-5d25da1a8d43
	golang.org/x/sync v0.0.0-20200625203802-6e8e738ad208
	google.golang.org/api v0.32.0
	gopkg.in/fsnotify.v1 v1.4.7
	gopkg.in/robfig/cron.v2 v2.0.0-20150107220207-be2e0b0deed5
	k8s.io/api v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v11.0.1-0.20190805182717-6502b5e7b1b5+incompatible
	k8s.io/klog/v2 v2.4.0
	k8s.io/test-infra v0.0.0-20210208210804-5e490580b8a6
	k8s.io/utils v0.0.0-20210111153108-fddb29f9d009
	sigs.k8s.io/boskos v0.0.0-20200617235605-f289ba6555ba
	sigs.k8s.io/controller-runtime v0.8.2-0.20210127230507-a763c9a36c6f
	sigs.k8s.io/controller-tools v0.3.0
	sigs.k8s.io/yaml v1.2.0
)
