module github.com/openshift/ci-tools

go 1.19

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v14.2.0+incompatible

	// forked version that compiles with k8s
	github.com/bombsimon/logrusr => github.com/stevekuznetsov/logrusr v1.1.1-0.20210709145202-301b9fbb8872
	github.com/containerd/containerd => github.com/containerd/containerd v0.2.10-0.20180716142608-408d13de2fbb

	github.com/docker/docker => github.com/openshift/moby-moby v1.4.2-0.20190308215630-da810a85109d

	// Forked version that disables diff trimming
	github.com/google/go-cmp => github.com/alvaroaleman/go-cmp v0.5.7-0.20210615160450-f8688cd5aaa0

	github.com/moby/buildkit => github.com/dmcgowan/buildkit v0.0.0-20170731200553-da2b9dc7dab9
	github.com/openshift/api => github.com/openshift/api v0.0.0-20201120165435-072a4cd8ca42
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20210730113412-1811c1b3fc0e
	github.com/openshift/library-go => github.com/openshift/library-go v0.0.0-20210826121606-162472d92388
	k8s.io/client-go => k8s.io/client-go v0.24.2
	k8s.io/component-base => k8s.io/component-base v0.24.2
	k8s.io/kubectl => k8s.io/kubectl v0.24.2
)

require (
	cloud.google.com/go/bigquery v1.8.0
	cloud.google.com/go/storage v1.22.1
	github.com/GoogleCloudPlatform/testgrid v0.0.123
	github.com/PagerDuty/go-pagerduty v1.4.1
	github.com/alecthomas/chroma v0.8.2-0.20201103103104-ab61726cdb54
	github.com/andygrunwald/go-jira v1.14.0
	github.com/blang/semver v3.5.1+incompatible
	github.com/bombsimon/logrusr/v3 v3.0.0
	github.com/docker/distribution v2.8.1+incompatible
	github.com/getlantern/deepcopy v0.0.0-20160317154340-7f45deb8130a
	github.com/ghodss/yaml v1.0.0
	github.com/go-ldap/ldap/v3 v3.4.1
	github.com/golang/mock v1.6.0
	github.com/google/go-cmp v0.5.8
	github.com/google/gofuzz v1.2.1-0.20210504230335-f78f29fc09ea
	github.com/hashicorp/go-retryablehttp v0.7.0
	github.com/hashicorp/go-version v1.2.1
	github.com/hashicorp/vault/api v1.0.4
	github.com/hashicorp/vault/sdk v0.1.13
	github.com/julienschmidt/httprouter v1.3.0
	github.com/kataras/tablewriter v0.0.0-20180708051242-e063d29b7c23
	github.com/mattn/go-zglob v0.0.2
	github.com/montanaflynn/stats v0.6.3
	github.com/openhistogram/circonusllhist v0.3.1-0.20210608220433-1bd1bfa6c998
	github.com/openshift-eng/openshift-goimports v0.0.0-20220201193023-4f8ea117352c
	github.com/openshift/api v0.0.0-20220531073726-6c4f186339a7
	github.com/openshift/builder v0.0.0-20200325182657-6a52122d21e0
	github.com/openshift/client-go v3.9.0+incompatible
	github.com/openshift/hive/apis v0.0.0-20220914191453-366466c2d2ca
	github.com/openshift/imagebuilder v1.1.1
	github.com/openshift/library-go v0.0.0-20200127110935-527e40ed17d9
	github.com/openshift/openshift-apiserver v0.0.0-alpha.0
	github.com/pkg/errors v0.9.1
	github.com/pmezard/go-difflib v1.0.0
	github.com/polyfloyd/go-errorlint v0.0.0-20210722154253-910bb7978349
	github.com/prometheus/client_golang v1.12.1
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.32.1
	github.com/prometheus/prometheus v2.5.0+incompatible
	github.com/satori/go.uuid v1.2.0
	github.com/sirupsen/logrus v1.8.1
	github.com/slack-go/slack v0.7.3
	github.com/spf13/afero v1.6.0
	github.com/spf13/cobra v1.4.0
	github.com/spf13/pflag v1.0.5
	golang.org/x/exp v0.0.0-20220722155223-a9213eeb770e
	// https://security.snyk.io/vuln/SNYK-GOLANG-GOLANGORGXNETHTTP2-3323837
	golang.org/x/net v0.7.0 // indirect
	golang.org/x/oauth2 v0.0.0-20220608161450-d0670ef3b1eb
	golang.org/x/sync v0.0.0-20220722155255-886fb9371eb4
	golang.org/x/text v0.7.0 // indirect
	google.golang.org/api v0.83.0
	gopkg.in/fsnotify.v1 v1.4.7
	gopkg.in/robfig/cron.v2 v2.0.0-20150107220207-be2e0b0deed5
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.24.2
	k8s.io/apimachinery v0.24.2
	k8s.io/apiserver v0.24.2
	k8s.io/client-go v11.0.1-0.20190805182717-6502b5e7b1b5+incompatible
	k8s.io/klog/v2 v2.70.1
	k8s.io/test-infra v0.0.0-20221121175430-dfec9ce18ddd
	k8s.io/utils v0.0.0-20220728103510-ee6ede2d64ed
	sigs.k8s.io/boskos v0.0.0-20210730172138-093b54882439
	sigs.k8s.io/controller-runtime v0.12.3
	sigs.k8s.io/controller-tools v0.9.2
	sigs.k8s.io/yaml v1.3.0
)

require (
	github.com/Azure/go-ansiterm v0.0.0-20210617225240-d185dfc1b5a1 // indirect
	github.com/Azure/go-autorest v14.2.0+incompatible // indirect
	github.com/Azure/go-autorest/autorest v0.11.27 // indirect
	github.com/Azure/go-autorest/autorest/adal v0.9.20 // indirect
	github.com/Azure/go-autorest/autorest/date v0.3.0 // indirect
	github.com/Azure/go-autorest/logger v0.2.1 // indirect
	github.com/Azure/go-autorest/tracing v0.6.0 // indirect
	github.com/Microsoft/go-winio v0.5.1 // indirect
	github.com/Nvveen/Gotty v0.0.0-20120604004816-cd527374f1e5 // indirect
	github.com/anaskhan96/soup v1.2.4
	github.com/aws/aws-sdk-go v1.38.49 // indirect
	github.com/bazelbuild/buildtools v0.0.0-20200922170545-10384511ce98 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bwmarrin/snowflake v0.0.0 // indirect
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/clarketm/json v1.13.4 // indirect
	github.com/danwakefield/fnmatch v0.0.0-20160403171240-cbb64ac3d964 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgrijalva/jwt-go/v4 v4.0.0-preview1 // indirect
	github.com/dlclark/regexp2 v1.2.0 // indirect
	github.com/docker/docker v20.10.12+incompatible // indirect
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.4.0 // indirect
	github.com/docker/libtrust v0.0.0-20160708172513-aabc10ec26b7 // indirect
	github.com/evanphx/json-patch v4.12.0+incompatible // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/fatih/structs v1.1.0 // indirect
	github.com/felixge/fgprof v0.9.1 // indirect
	github.com/fsnotify/fsnotify v1.5.1 // indirect
	github.com/fsouza/go-dockerclient v1.6.3 // indirect
	github.com/fvbommel/sortorder v1.0.1 // indirect
	github.com/go-logr/logr v1.2.3 // indirect
	github.com/gobuffalo/flect v0.2.5 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/golang/snappy v0.0.3 // indirect
	github.com/gomodule/redigo v1.8.5 // indirect
	github.com/google/btree v1.0.1 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/pprof v0.0.0-20210720184732-4bb14d4b1be1 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/google/wire v0.4.0 // indirect
	github.com/googleapis/gax-go v2.0.2+incompatible // indirect
	github.com/googleapis/gax-go/v2 v2.4.0 // indirect
	github.com/gorilla/websocket v1.4.2 // indirect
	github.com/gregjones/httpcache v0.0.0-20190611155906-901d90724c79 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.2 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/imdario/mergo v0.3.12 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/magiconair/properties v1.8.5 // indirect
	github.com/mattn/go-colorable v0.1.11 // indirect
	github.com/mattn/go-isatty v0.0.14 // indirect
	github.com/mattn/go-runewidth v0.0.9 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.2-0.20181231171920-c182affec369 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.4.2 // indirect
	github.com/moby/spdystream v0.2.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.0.3-0.20220114050600-8b9d41f48198 // indirect
	github.com/opencontainers/runc v1.1.3 // indirect
	github.com/pelletier/go-toml v1.9.4 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/pierrec/lz4 v2.2.6+incompatible // indirect
	github.com/prometheus/procfs v0.7.3 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/shurcooL/githubv4 v0.0.0-20210725200734-83ba7b4c9228
	github.com/shurcooL/graphql v0.0.0-20181231061246-d48a9a75455f
	github.com/spf13/cast v1.4.1 // indirect
	github.com/spf13/jwalterweatherman v1.1.0 // indirect
	github.com/spf13/viper v1.9.0 // indirect
	github.com/subosito/gotenv v1.2.0 // indirect
	github.com/tektoncd/pipeline v0.36.0 // indirect
	github.com/trivago/tgo v1.0.7 // indirect
	go.opencensus.io v0.23.0 // indirect
	go4.org v0.0.0-20201209231011-d4a079459e60 // indirect
	gocloud.dev v0.19.0 // indirect
	golang.org/x/crypto v0.0.0-20220315160706-3147a52a75dd // indirect
	golang.org/x/lint v0.0.0-20210508222113-6edffad5e616 // indirect
	golang.org/x/mod v0.6.0-dev.0.20220419223038-86c51ed26bb4 // indirect
	golang.org/x/sys v0.5.0 // indirect
	golang.org/x/term v0.5.0 // indirect
	golang.org/x/time v0.0.0-20220411224347-583f2d630306 // indirect
	golang.org/x/tools v0.1.12 // indirect
	golang.org/x/xerrors v0.0.0-20220609144429-65e65417b02f // indirect
	gomodules.xyz/jsonpatch/v2 v2.2.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto v0.0.0-20220608133413-ed9918b62aac // indirect
	google.golang.org/grpc v1.47.0 // indirect
	google.golang.org/protobuf v1.28.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/ini.v1 v1.63.2
	gopkg.in/square/go-jose.v2 v2.3.1 // indirect
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/apiextensions-apiserver v0.24.2 // indirect
	k8s.io/component-base v0.24.2 // indirect
	k8s.io/kube-openapi v0.0.0-20220803162953-67bda5d908f1 // indirect
	k8s.io/kubernetes v1.25.3 // indirect
	knative.dev/pkg v0.0.0-20220329144915-0a1ec2e0d46c // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.3 // indirect
)

require github.com/stretchr/testify v1.8.0

require (
	bitbucket.org/creachadair/stringset v0.0.9 // indirect
	cloud.google.com/go v0.102.0 // indirect
	cloud.google.com/go/compute v1.6.1 // indirect
	cloud.google.com/go/iam v0.3.0 // indirect
	contrib.go.opencensus.io/exporter/ocagent v0.7.1-0.20200907061046-05415f1de66d // indirect
	contrib.go.opencensus.io/exporter/prometheus v0.4.0 // indirect
	github.com/Azure/go-ntlmssp v0.0.0-20200615164410-66371956d46c // indirect
	github.com/ProtonMail/go-crypto v0.0.0-20210428141323-04723f9f07d7 // indirect
	github.com/PuerkitoBio/purell v1.1.1 // indirect
	github.com/PuerkitoBio/urlesc v0.0.0-20170810143723-de5bf2ad4578 // indirect
	github.com/acomagu/bufpipe v1.0.3 // indirect
	github.com/andygrunwald/go-gerrit v0.0.0-20210709065208-9d38b0be0268 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/blendle/zapdriver v1.3.1 // indirect
	github.com/census-instrumentation/opencensus-proto v0.3.0 // indirect
	github.com/denormal/go-gitignore v0.0.0-20180930084346-ae8ad1d07817 // indirect
	github.com/emicklei/go-restful/v3 v3.8.0 // indirect
	github.com/emirpasic/gods v1.12.0 // indirect
	github.com/evanphx/json-patch/v5 v5.6.0 // indirect
	github.com/go-asn1-ber/asn1-ber v1.5.1 // indirect
	github.com/go-git/gcfg v1.5.0 // indirect
	github.com/go-git/go-billy/v5 v5.3.1 // indirect
	github.com/go-git/go-git/v5 v5.4.2 // indirect
	github.com/go-kit/log v0.1.0 // indirect
	github.com/go-logfmt/logfmt v0.5.0 // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.19.6 // indirect
	github.com/go-openapi/swag v0.21.1 // indirect
	github.com/golang-jwt/jwt v3.2.1+incompatible // indirect
	github.com/golang-jwt/jwt/v4 v4.3.0 // indirect
	github.com/google/gnostic v0.5.7-v3refs // indirect
	github.com/google/go-containerregistry v0.8.1-0.20220216220642-00c59d91847c // indirect
	github.com/googleapis/go-type-adapters v1.0.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.16.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/kevinburke/ssh_config v0.0.0-20201106050909-4977a11b4351 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/openshift/custom-resource-status v1.1.3-0.20220503160415-f2fdb4999d87 // indirect
	github.com/prometheus/statsd_exporter v0.21.0 // indirect
	github.com/sergi/go-diff v1.1.0 // indirect
	github.com/xanzy/ssh-agent v0.3.0 // indirect
	go.uber.org/atomic v1.9.0 // indirect
	go.uber.org/multierr v1.7.0 // indirect
	go.uber.org/zap v1.19.1 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	sigs.k8s.io/json v0.0.0-20220713155537-f223a00ba0e2 // indirect
)

replace github.com/opencontainers/runc => github.com/opencontainers/runc v1.0.0-rc9
