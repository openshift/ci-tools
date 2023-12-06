package main

import (
	"context"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/logrusutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/prowconfigutils"
)

type Page struct {
	Data []map[string]string `json:"data"`
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.IntVar(&o.port, "port", 8090, "Port to run the server on")
	o.kubernetesOptions.AddFlags(fs)
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func validateOptions(o options) error {
	_, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	return o.kubernetesOptions.Validate(false)
}

type options struct {
	logLevel          string
	port              int
	gracePeriod       time.Duration
	kubernetesOptions flagutil.KubernetesOptions
}

func addSchemes() error {
	if err := hivev1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add hivev1 to scheme: %w", err)
	}
	if err := routev1.Install(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add routev1 to scheme: %w", err)
	}
	if err := configv1.Install(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add configv1 to scheme: %w", err)
	}
	return nil
}

func getClusterPool(ctx context.Context, hiveClient ctrlruntimeclient.Client) ([]map[string]string, error) {
	logrus.Debug("Calling getClusterPool ...")
	clusterImageSetMap := map[string]string{}
	clusterImageSets := &hivev1.ClusterImageSetList{}
	if err := hiveClient.List(ctx, clusterImageSets); err != nil {
		return nil, fmt.Errorf("failed to list cluster image sets: %w", err)
	}
	for _, i := range clusterImageSets.Items {
		clusterImageSetMap[i.Name] = i.Spec.ReleaseImage
	}

	clusterPools := &hivev1.ClusterPoolList{}
	if err := hiveClient.List(ctx, clusterPools); err != nil {
		return nil, fmt.Errorf("failed to list cluster pools: %w", err)
	}

	data := make([]map[string]string, 0)
	for _, p := range clusterPools.Items {
		maxSize := "nil"
		if p.Spec.MaxSize != nil {
			maxSize = strconv.FormatInt(int64(*p.Spec.MaxSize), 10)
		}
		releaseImage := clusterImageSetMap[p.Spec.ImageSetRef.Name]
		owner := p.Labels["owner"]
		data = append(data, map[string]string{
			"namespace":    p.Namespace,
			"name":         p.Name,
			"ready":        strconv.FormatInt(int64(p.Status.Ready), 10),
			"size":         strconv.FormatInt(int64(p.Spec.Size), 10),
			"maxSize":      maxSize,
			"imageSet":     p.Spec.ImageSetRef.Name,
			"labels":       labels.FormatLabels(p.Labels),
			"releaseImage": releaseImage,
			"owner":        owner,
			"standby":      strconv.FormatInt(int64(p.Status.Standby), 10),
		},
		)
	}
	return data, nil
}

type ClusterInfoGetter interface {
	GetClusterDetails(ctx context.Context, cluster string, client ctrlruntimeclient.Client) (map[string]string, error)
}

type clusterInfoGetter struct{}

type hypershiftSupportedVersions struct {
	Versions []string `json:"versions"`
}

func (g *clusterInfoGetter) GetClusterDetails(ctx context.Context, cluster string, client ctrlruntimeclient.Client) (map[string]string, error) {
	consoleHost, err := api.ResolveConsoleHost(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the console host for cluster %s: %w", cluster, err)
	}
	registryHost, err := api.ResolveImageRegistryHost(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the image registry host for cluster %s: %w", cluster, err)
	}
	cv := &configv1.ClusterVersion{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "version"}, cv); err != nil {
		return nil, fmt.Errorf("failed to get ClusterVersion for cluster %s: %w", cluster, err)
	}
	if len(cv.Status.History) == 0 {
		return nil, fmt.Errorf("failed to get ClusterVersion for cluster %s: no history found", cluster)
	}
	infra := &configv1.Infrastructure{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "cluster"}, infra); err != nil {
		return nil, fmt.Errorf("failed to get Infrastructure for cluster %s: %w", cluster, err)
	}
	version := cv.Status.History[0].Version
	product, err := resolveProduct(ctx, client, version)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the product for cluster %s: %w", cluster, err)
	}
	cloud := string(infra.Status.PlatformStatus.Type)

	ret := map[string]string{
		"cluster":      cluster,
		"consoleHost":  consoleHost,
		"registryHost": registryHost,
		"version":      version,
		"product":      product,
		"cloud":        cloud,
	}
	if cluster == "hive" {
		hypershiftCM := &corev1.ConfigMap{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "hypershift", Name: "supported-versions"}, hypershiftCM); err != nil {
			return nil, fmt.Errorf("failed to get ConfigMap supported-versions in hypershift for cluster hive: %w", err)
		}
		versions, ok := hypershiftCM.Data["supported-versions"]
		if !ok {
			return nil, fmt.Errorf("failed to get supported-versions from ConfigMap supported-versions in hypershift for cluster hive: %w", err)
		}

		sVersions := &hypershiftSupportedVersions{}
		if err := json.Unmarshal([]byte(versions), sVersions); err != nil {
			return nil, fmt.Errorf("failed to unmarshal hive's supported versions: %w", err)
		}
		d, err := json.Marshal(sVersions.Versions)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal hive's supported versions: %w", err)
		}
		ret["hypershiftSupportedVersions"] = string(d)
	}

	return ret, nil
}

type memoryCache struct {
	clusterDataMutex         sync.Mutex
	clusterData              []map[string]string
	clusterDataLastUpdatedAt time.Time

	ClusterPoolDataMutex         sync.Mutex
	ClusterPoolData              []map[string]string
	ClusterPoolDataLastUpdatedAt time.Time

	CacheDuration time.Duration
}

func (c *memoryCache) GetClusterPoolPage(ctx context.Context, client ctrlruntimeclient.Client) (*Page, error) {
	c.ClusterPoolDataMutex.Lock()
	defer c.ClusterPoolDataMutex.Unlock()
	if c.ClusterPoolData == nil || time.Now().After(c.ClusterPoolDataLastUpdatedAt.Add(c.CacheDuration)) {
		data, err := getClusterPool(ctx, client)
		if err != nil {
			if c.ClusterPoolData == nil {
				return nil, err
			} else {
				logrus.WithError(err).Error("Failed to get cluster pool, using the expired cached data")
			}
		} else {
			c.ClusterPoolDataLastUpdatedAt = time.Now()
			c.ClusterPoolData = data
		}
	}
	return &Page{Data: c.ClusterPoolData}, nil
}

func (c *memoryCache) GetClusterPage(ctx context.Context, clients map[string]ctrlruntimeclient.Client, skipHive bool, getter ClusterInfoGetter) *Page {
	c.clusterDataMutex.Lock()
	defer c.clusterDataMutex.Unlock()
	if c.clusterData == nil || time.Now().After(c.clusterDataLastUpdatedAt.Add(c.CacheDuration)) {
		data := getCluster(ctx, clients, getter)
		c.clusterDataLastUpdatedAt = time.Now()
		c.clusterData = data
	}
	if skipHive {
		var skipHiveData []map[string]string
		for _, d := range c.clusterData {
			var hive bool
			for k, v := range d {
				if k == "cluster" && v == string(api.HiveCluster) {
					hive = true
					break
				}
			}
			if !hive {
				skipHiveData = append(skipHiveData, d)
			}
		}
		return &Page{Data: skipHiveData}
	}
	return &Page{Data: c.clusterData}
}

func getCluster(ctx context.Context, clients map[string]ctrlruntimeclient.Client, getter ClusterInfoGetter) []map[string]string {
	logrus.Debug("Calling getCluster ...")
	var data []map[string]string

	for cluster, client := range clients {
		clusterInfo, err := getter.GetClusterDetails(ctx, cluster, client)
		if err != nil {
			logrus.WithError(err).Warn("Failed to get cluster details")
			clusterInfo = map[string]string{"cluster": cluster, "error": "an error occurred while retrieving cluster information"}
		}
		data = append(data, clusterInfo)
	}

	sort.Slice(data, func(i, j int) bool {
		return data[i]["cluster"] < data[j]["cluster"]
	})
	return data
}

func resolveProduct(ctx context.Context, client ctrlruntimeclient.Client, version string) (string, error) {
	ns := "openshift-monitoring"
	name := "configure-alertmanager-operator"
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, &corev1.Service{}); err != nil {
		if !kerrors.IsNotFound(err) {
			return "", fmt.Errorf("failed to get Service %s in namespace %s: %w", name, ns, err)
		}
		if strings.Contains(version, "okd") {
			return strings.ToUpper(string(api.ReleaseProductOKD)), nil
		}
		return strings.ToUpper(string(api.ReleaseProductOCP)), nil
	}
	return "OSD", nil
}

func getRouter(ctx context.Context, hiveClient ctrlruntimeclient.Client, clients map[string]ctrlruntimeclient.Client, prowDisabledClusters []string) *http.ServeMux {
	handler := http.NewServeMux()
	cache := memoryCache{CacheDuration: time.Hour}

	handler.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page := map[string]bool{"ok": true}
		if err := json.NewEncoder(w).Encode(page); err != nil {
			logrus.WithError(err).WithField("page", page).Error("failed to encode page")
		}
	})

	allClients := map[string]ctrlruntimeclient.Client{string(api.HiveCluster): hiveClient}
	for cluster, client := range clients {
		allClients[cluster] = client
	}
	writeRespond := func(crd string, w http.ResponseWriter, r *http.Request) {
		var page *Page
		var err error
		switch crd {
		case "clusterpools":
			page, err = cache.GetClusterPoolPage(ctx, hiveClient)
		case "clusters":
			skipHive := r.URL.Query().Get("skipHive") == "true"
			disabledClusters := sets.New[string](prowDisabledClusters...)
			page = cache.GetClusterPage(ctx, allClients, skipHive, &clusterInfoGetter{})
			var enabled []map[string]string
			for _, d := range page.Data {
				c, ok := d["cluster"]
				if ok && !disabledClusters.Has(c) {
					enabled = append(enabled, d)
				}
			}
			page.Data = enabled
			noDups := disabledClusters.UnsortedList()
			sort.Strings(noDups)
			for _, cluster := range noDups {
				page.Data = append(page.Data, map[string]string{"cluster": cluster, "error": "disabled cluster in Prow"})
			}
		default:
			http.Error(w, fmt.Sprintf("Unknown crd: %s", crd), http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if callbackName := r.URL.Query().Get("callback"); callbackName != "" {
			bytes, err := json.Marshal(page)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/javascript")
			content := string(bytes)
			template.JSEscape(w, []byte(callbackName))
			if n, err := fmt.Fprintf(w, "(%s);", content); err != nil {
				logrus.WithError(err).WithField("n", n).WithField("content", content).Error("failed to write content")
			}
			return
		} else {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(page); err != nil {
				logrus.WithError(err).WithField("page", page).Error("failed to encode page")
			}
			return
		}
	}

	handler.HandleFunc("/api/v1/clusterpools", func(w http.ResponseWriter, r *http.Request) {
		logrus.WithField("path", "/api/v1/clusterpools").Info("serving")
		writeRespond("clusterpools", w, r)
	})

	handler.HandleFunc("/api/v1/clusters", func(w http.ResponseWriter, r *http.Request) {
		logrus.WithField("path", "/api/v1/clusters").Info("serving")
		writeRespond("clusters", w, r)
	})
	return handler
}

const (
	appCIContextName = string(api.ClusterAPPCI)
)

func main() {
	logrusutil.ComponentInit()
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed go gather options")
	}
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	prowDisabledClusters, err := prowconfigutils.ProwDisabledClusters(&o.kubernetesOptions)
	if err != nil {
		logrus.WithError(err).Warn("Failed to get Prow disable clusters")
	}

	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)

	if err := addSchemes(); err != nil {
		logrus.WithError(err).Fatal("failed to set up scheme")
	}

	kubeconfigChangedCallBack := func() {
		logrus.Info("Kubeconfig changed, exiting to get restarted by Kubelet and pick up the changes")
		interrupts.Terminate()
	}

	kubeConfigs, err := o.kubernetesOptions.LoadClusterConfigs(kubeconfigChangedCallBack)
	if err != nil {
		logrus.WithError(err).Fatal("could not load kube config")
	}

	inClusterConfig, hasInClusterConfig := kubeConfigs[kube.InClusterContext]
	delete(kubeConfigs, kube.InClusterContext)
	delete(kubeConfigs, kube.DefaultClusterAlias)

	if _, hasAppCi := kubeConfigs[appCIContextName]; !hasAppCi {
		if !hasInClusterConfig {
			logrus.WithField("context", appCIContextName).WithError(err).Fatal("failed to find context and loading InClusterConfig failed")
		}
		logrus.WithField("context", appCIContextName).Info("use InClusterConfig for context")
		kubeConfigs[appCIContextName] = inClusterConfig
	}

	hiveConfig, ok := kubeConfigs[string(api.HiveCluster)]
	if !ok {
		logrus.WithField("context", string(api.HiveCluster)).WithError(err).Fatal("failed to find context")
	}
	hiveClient, err := ctrlruntimeclient.New(&hiveConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatal("could not get Hive client for Hive kube config")
	}

	clients := map[string]ctrlruntimeclient.Client{}
	for cluster, kubeconfig := range kubeConfigs {
		cluster, kubeconfig := cluster, kubeconfig
		if cluster == string(api.HiveCluster) {
			continue
		}
		client, err := ctrlruntimeclient.New(&kubeconfig, ctrlruntimeclient.Options{})
		if err != nil {
			logrus.WithField("cluster", cluster).WithError(err).Fatal("could not get client for kube config")
		}
		clients[cluster] = client
	}

	server := &http.Server{
		Addr:    ":" + strconv.Itoa(o.port),
		Handler: getRouter(interrupts.Context(), hiveClient, clients, prowDisabledClusters),
	}
	interrupts.ListenAndServe(server, o.gracePeriod)
	interrupts.WaitForGracefulShutdown()
}
