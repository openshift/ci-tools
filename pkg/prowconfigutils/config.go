package prowconfigutils

import (
	"fmt"
	"io"
	"net/http"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
)

// ProwDisabledClusters returns the disabled clusters in Prow and sets disable clusters on the given KubernetesOptions
func ProwDisabledClusters(o *flagutil.KubernetesOptions) (ret []string, retErr error) {
	prowDisabledClusters := sets.New[string]()
	ret, retErr = disabledClusters(fmt.Sprintf("%s/config?key=disabled-clusters", api.URLForService(api.ServiceProw)))
	if retErr == nil && len(ret) > 0 {
		prowDisabledClusters.Insert(ret...)
		logrus.WithField("prowDisabledClusters", prowDisabledClusters.UnsortedList()).Warn("Some clusters are disabled in Prow's configuration")
	}
	if retErr == nil && o != nil {
		logrus.WithField("prowDisabledClusters", prowDisabledClusters.UnsortedList()).Info("Setting disabled clusters on KubernetesOptions ...")
		o.SetDisabledClusters(prowDisabledClusters)
	}
	return ret, retErr
}

func disabledClusters(url string) (ret []string, retErr error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do http request: %w", err)
	}
	defer func() {
		if tempErr := resp.Body.Close(); tempErr != nil {
			logrus.WithError(err).Error("Failed to close response body")
			ret = nil
			retErr = tempErr
		}
	}()

	if statusCode := resp.StatusCode; statusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", statusCode)
	}
	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if err := yaml.Unmarshal(bytes, &ret); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}
	return ret, err
}
