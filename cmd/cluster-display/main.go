package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/util"
)

type Page struct {
	Data []map[string]string `json:"data"`
}

func gatherOptions() (options, error) {
	o := options{kubernetesOptions: flagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.IntVar(&o.port, "port", 8090, "Port to run the server on")
	fs.StringVar(&o.hiveKubeconfigPath, "hive-kubeconfig", "", "Path to the kubeconfig file to use for requests to Hive.")
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
	logLevel           string
	port               int
	gracePeriod        time.Duration
	kubernetesOptions  flagutil.KubernetesOptions
	hiveKubeconfigPath string
}

func addSchemes() error {
	if err := hivev1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add hivev1 to scheme: %w", err)
	}
	return nil
}

func getClusterPoolPage(ctx context.Context, hiveClient ctrlruntimeclient.Client) (*Page, error) {
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

	page := Page{Data: []map[string]string{}}
	for _, p := range clusterPools.Items {
		maxSize := "nil"
		if p.Spec.MaxSize != nil {
			maxSize = strconv.FormatInt(int64(*p.Spec.MaxSize), 10)
		}
		releaseImage := clusterImageSetMap[p.Spec.ImageSetRef.Name]
		owner := p.Labels["owner"]
		page.Data = append(
			page.Data, map[string]string{
				"namespace":    p.Namespace,
				"name":         p.Name,
				"ready":        strconv.FormatInt(int64(p.Status.Ready), 10),
				"size":         strconv.FormatInt(int64(p.Spec.Size), 10),
				"maxSize":      maxSize,
				"imageSet":     p.Spec.ImageSetRef.Name,
				"labels":       labels.FormatLabels(p.Labels),
				"releaseImage": releaseImage,
				"owner":        owner,
			},
		)
	}
	return &page, nil
}

func getRouter(ctx context.Context, hiveClient ctrlruntimeclient.Client) *http.ServeMux {
	handler := http.NewServeMux()

	handler.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page := map[string]bool{"ok": true}
		if err := json.NewEncoder(w).Encode(page); err != nil {
			logrus.WithError(err).WithField("page", page).Error("failed to encode page")
		}
	})

	handler.HandleFunc("/api/v1/clusterpools", func(w http.ResponseWriter, r *http.Request) {
		logrus.WithField("path", "/api/v1/clusterpools").Info("serving")
		page, err := getClusterPoolPage(ctx, hiveClient)
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
			if n, err := fmt.Fprintf(w, "%s(%s);", callbackName, content); err != nil {
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
	})
	return handler
}

func main() {
	logrusutil.ComponentInit()
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed go gather options")
	}
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatalf("invalid options")
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

	var hiveConfig *rest.Config
	if o.hiveKubeconfigPath == "" {
		kubeConfigs, err := o.kubernetesOptions.LoadClusterConfigs(kubeconfigChangedCallBack)
		if err != nil {
			logrus.WithError(err).Fatal("could not load kube config")
		}
		kubeConfig, ok := kubeConfigs[string(api.HiveCluster)]
		if len(kubeConfigs) != 1 || !ok {
			logrus.WithError(err).Fatalf("found %d contexts in Hive kube config and it must be %s", len(kubeConfigs), string(api.HiveCluster))
		}
		hiveConfig = &kubeConfig
	} else {
		// This branch and o.hiveKubeconfigPath will be removed after migration
		kubeConfigs, err := util.LoadKubeConfigs(o.hiveKubeconfigPath, "", nil)
		if err != nil {
			logrus.WithError(err).WithField("o.hiveKubeconfigPath", o.hiveKubeconfigPath).Fatal("could not load Hive kube config")
		}
		kubeConfig, ok := kubeConfigs[string(api.HiveCluster)]
		if len(kubeConfigs) != 1 || !ok {
			logrus.WithError(err).WithField("o.hiveKubeconfigPath", o.hiveKubeconfigPath).Fatalf("found %d contexts in Hive kube config and it must be %s", len(kubeConfigs), string(api.HiveCluster))
		}
		hiveConfig = kubeConfig
	}

	hiveClient, err := ctrlruntimeclient.New(hiveConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatal("could not get Hive client for Hive kube config")
	}
	server := &http.Server{
		Addr:    ":" + strconv.Itoa(o.port),
		Handler: getRouter(interrupts.Context(), hiveClient),
	}
	interrupts.ListenAndServe(server, o.gracePeriod)
	interrupts.WaitForGracefulShutdown()
}
