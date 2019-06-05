module github.com/openshift/ci-operator-prowgen

replace github.com/golang/lint => golang.org/x/lint v0.0.0-20190301231843-5614ed5bae6f

require (
	cloud.google.com/go v0.37.4
	github.com/getlantern/deepcopy v0.0.0-20160317154340-7f45deb8130a
	github.com/ghodss/yaml v1.0.0
	github.com/mattn/go-zglob v0.0.1
	github.com/openshift/ci-operator v0.0.0-20190523203517-fc248912d39f
	github.com/qor/inflection v0.0.0-20180308033659-04140366298a // indirect
	github.com/shurcooL/githubv4 v0.0.0-20180925043049-51d7b505e2e9
	github.com/sirupsen/logrus v1.4.2
	golang.org/x/oauth2 v0.0.0-20190226205417-e64efc72b421
	golang.org/x/sync v0.0.0-20190423024810-112230192c58
	google.golang.org/api v0.3.2
	k8s.io/api v0.0.0-20181128191700-6db15a15d2d3
	k8s.io/apimachinery v0.0.0-20181128191346-49ce2735e507
	k8s.io/client-go v9.0.0+incompatible
	k8s.io/test-infra v0.0.0-20190605204812-45f60c377626
)
