package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"text/template"
	"time"

	"github.com/openhistogram/circonusllhist"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	prowConfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/simplifypath"

	"github.com/openshift/ci-tools/pkg/api"
)

var (
	//go:embed style.css
	styleCSS []byte

	//go:embed index.js
	indexJS []byte

	//go:embed circllhist.js
	circllhistJS []byte

	//go:embed index.template.html
	indexTemplateRaw []byte

	indexTemplate = template.Must(template.New("index").Parse(string(indexTemplateRaw)))
)

// l keeps the tree legible
func l(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.L(fragment, children...)
}

const (
	OrgQuery       = "org"
	RepoQuery      = "repo"
	BranchQuery    = "branch"
	VariantQuery   = "variant"
	TargetQuery    = "target"
	StepQuery      = "step"
	PodQuery       = "pod"
	ContainerQuery = "container"
)

func metadataFromQuery(w http.ResponseWriter, r *http.Request) (FullMetadata, error) {
	meta := FullMetadata{
		Metadata: api.Metadata{},
	}
	for _, entry := range []struct {
		query    string
		into     *string
		optional bool
	}{
		{query: OrgQuery, into: &meta.Metadata.Org},
		{query: RepoQuery, into: &meta.Metadata.Repo},
		{query: BranchQuery, into: &meta.Metadata.Branch},
		{query: VariantQuery, into: &meta.Metadata.Variant, optional: true},
		{query: TargetQuery, into: &meta.Target},
		{query: StepQuery, into: &meta.Step, optional: true},
		{query: PodQuery, into: &meta.Pod},
		{query: ContainerQuery, into: &meta.Container},
	} {
		value := r.URL.Query().Get(entry.query)
		if value == "" && !entry.optional {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "%s query missing", entry.query)
			return FullMetadata{}, fmt.Errorf("missing query %q", entry.query)
		}
		*entry.into = value
	}
	return meta, nil
}

func stepMetadataFromQuery(w http.ResponseWriter, r *http.Request) (StepMetadata, error) {
	meta := StepMetadata{}
	for _, entry := range []struct {
		query string
		into  *string
	}{
		{query: StepQuery, into: &meta.Step},
		{query: ContainerQuery, into: &meta.Container},
	} {
		value := r.URL.Query().Get(entry.query)
		if value == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "%s query missing", entry.query)
			return StepMetadata{}, fmt.Errorf("missing query %q", entry.query)
		}
		*entry.into = value
	}
	return meta, nil
}

var (
	uiMetrics = metrics.NewMetrics("pod_scaler_ui")
)

func serveUI(cpu, memory []*cacheReloader, port int) {
	logger := logrus.WithField("component", "frontend")
	server := &frontendServer{
		logger:          logger,
		lock:            sync.RWMutex{},
		indexedMetadata: map[string]map[string]map[string]map[string]map[string]containerMeta{},
		byMetaData:      map[FullMetadata]map[corev1.ResourceName]dataForDisplay{},
	}
	var infos []digestInfo
	for i := range cpu {
		infos = append(infos, digestInfo{name: cpu[i].name, data: cpu[i], digest: server.digestCPU})
	}
	for i := range memory {
		infos = append(infos, digestInfo{name: memory[i].name, data: memory[i], digest: server.digestMemory})
	}
	loadDone := digest(logger, infos...)
	interrupts.Run(func(ctx context.Context) {
		select {
		case <-ctx.Done():
			logger.Debug("Waiting for readiness cancelled.")
			return
		case <-loadDone:
			logger.Debugf("Ready to serve UI requests.")
		}
	})

	metrics.ExposeMetrics("pod-scaler", prowConfig.PushGateway{}, flagutil.DefaultMetricsPort)
	simplifier := simplifypath.NewSimplifier(l("", // shadow element mimicing the root
		l(""), // actual UI
		l("index.js"),
		l("circllhist.js"),
		l("style.css"),
		l("favicon.ico"),
		l("data"),
	))
	handler := metrics.TraceHandler(simplifier, uiMetrics.HTTPRequestDuration, uiMetrics.HTTPResponseSize)
	mux := http.NewServeMux()
	mux.HandleFunc("/", handler(getUI(server)).ServeHTTP)
	mux.HandleFunc("/index.js", handler(staticFileHandler(indexJS, "text/javascript")).ServeHTTP)
	mux.HandleFunc("/circllhist.js", handler(staticFileHandler(circllhistJS, "text/javascript")).ServeHTTP)
	mux.HandleFunc("/style.css", handler(staticFileHandler(styleCSS, "text/css")).ServeHTTP)
	mux.HandleFunc("/data", handler(getSpecificData(server)).ServeHTTP)
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: mux}
	interrupts.ListenAndServe(httpServer, 5*time.Second)
	logger.Debug("Ready to serve HTTP requests.")
}

type frontendServer struct {
	logger *logrus.Entry
	lock   sync.RWMutex
	// indexedMetadata is a cache of all metadata we've seen, indexed by parent/child
	// relationships in the hierarchy. This structure allows for simple auto-fills of
	// drop-down menus for users drilling down into a specific entry.
	//                  org->      repo->     branch->   variant->  target
	indexedMetadata map[string]map[string]map[string]map[string]map[string]containerMeta

	// byMetaData caches display data calculated for the full assortment of
	// metadata labels.
	byMetaData map[FullMetadata]map[corev1.ResourceName]dataForDisplay
}

// containerMeta caches container names and their owning Pods or Steps.
// There's only every one Pod for a step, but not all Pods are made for Steps.
type containerMeta struct {
	ContainersByStep map[string][]string `json:"containers_by_step"`
	ContainersByPod  map[string][]string `json:"containers_by_pod"`
}

// dataForDisplay caches precomputed values for displaying data
type dataForDisplay struct {
	Cutoff     float64                     `json:"cutoff"`
	Merged     *circonusllhist.Histogram   `json:"merged"`
	Histograms []*circonusllhist.Histogram `json:"histograms"`
}

func staticFileHandler(content []byte, mimeType string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Content-Type", mimeType)
		if _, err := w.Write(content); err != nil {
			logrus.WithError(err).Error("failed to write response")
		}
	}
}

func getUI(server *frontendServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		server.lock.RLock()
		data := server.indexedMetadata
		server.lock.RUnlock()
		raw, err := json.Marshal(data)
		if err != nil {
			metrics.RecordError("failed to marshal index data", uiMetrics.ErrorRate)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to marshal index data to JSON: %v", err)
			server.logger.WithError(err).Errorf("Failed to marshal index data to JSON.")
			return
		}
		if err := indexTemplate.Execute(w, string(raw)); err != nil {
			server.logger.WithError(err).Error("failed to execute template response")
		}
	}
}

func getSpecificData(server *frontendServer) http.HandlerFunc {
	return getData(func(w http.ResponseWriter, r *http.Request) (*logrus.Entry, map[corev1.ResourceName]dataForDisplay, bool, error) {
		meta, err := metadataFromQuery(w, r)
		if err != nil {
			return nil, nil, false, err
		}
		logger := logrus.WithFields(meta.LogFields())
		server.lock.RLock()
		data, found := server.byMetaData[meta]
		server.lock.RUnlock()
		return logger, data, found, nil
	})
}

func getData(resolve func(w http.ResponseWriter, r *http.Request) (*logrus.Entry, map[corev1.ResourceName]dataForDisplay, bool, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return
		}
		logger, data, found, err := resolve(w, r)
		if err != nil {
			metrics.RecordError("invalid query", uiMetrics.ErrorRate)
			return
		}
		if !found {
			metrics.RecordError("data not found", uiMetrics.ErrorRate)
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "no data available")
			logger.Warning("No data found.")
			return
		}
		raw, err := json.Marshal(data)
		if err != nil {
			metrics.RecordError("failed to marshal data", uiMetrics.ErrorRate)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to marshal data to JSON: %v", err)
			logger.WithError(err).Errorf("Failed to marshal data to JSON.")
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(raw); err != nil {
			logrus.WithError(err).Error("Failed to write response")
		}
	}
}

func (s *frontendServer) digestCPU(data *CachedQuery) {
	s.logger.Debugf("Digesting new CPU consumption metrics.")
	s.digestData(data, corev1.ResourceCPU, cpuRequestQuantile)
}

func (s *frontendServer) digestMemory(data *CachedQuery) {
	s.logger.Debugf("Digesting new CPU consumption metrics.")
	s.digestData(data, corev1.ResourceMemory, memRequestQuantile)
}

func (s *frontendServer) digestData(data *CachedQuery, metric corev1.ResourceName, quantile float64) {
	s.logger.Debugf("Digesting %d identifiers.", len(data.DataByMetaData))
	i := 0
	for meta, fingerprints := range data.DataByMetaData {
		if i%(len(data.DataByMetaData)/10) == 0 {
			s.logger.Debugf("Digested %d/%d full identifiers.", i, len(data.DataByMetaData))
		}
		i += 1
		s.lock.Lock()
		if _, exists := s.indexedMetadata[meta.Org]; !exists {
			s.indexedMetadata[meta.Org] = map[string]map[string]map[string]map[string]containerMeta{}
		}
		if _, exists := s.indexedMetadata[meta.Org][meta.Repo]; !exists {
			s.indexedMetadata[meta.Org][meta.Repo] = map[string]map[string]map[string]containerMeta{}
		}
		if _, exists := s.indexedMetadata[meta.Org][meta.Repo][meta.Branch]; !exists {
			s.indexedMetadata[meta.Org][meta.Repo][meta.Branch] = map[string]map[string]containerMeta{}
		}
		if _, exists := s.indexedMetadata[meta.Org][meta.Repo][meta.Branch][meta.Variant]; !exists {
			s.indexedMetadata[meta.Org][meta.Repo][meta.Branch][meta.Variant] = map[string]containerMeta{}
		}
		if _, exists := s.indexedMetadata[meta.Org][meta.Repo][meta.Branch][meta.Variant][meta.Target]; !exists {
			s.indexedMetadata[meta.Org][meta.Repo][meta.Branch][meta.Variant][meta.Target] = containerMeta{
				ContainersByStep: map[string][]string{},
				ContainersByPod:  map[string][]string{},
			}
		}
		if meta.Step != "" {
			current := s.indexedMetadata[meta.Org][meta.Repo][meta.Branch][meta.Variant][meta.Target].ContainersByStep[meta.Step]
			updated := sets.NewString(current...)
			updated.Insert(meta.Container)
			s.indexedMetadata[meta.Org][meta.Repo][meta.Branch][meta.Variant][meta.Target].ContainersByStep[meta.Step] = updated.List()
		}
		current := s.indexedMetadata[meta.Org][meta.Repo][meta.Branch][meta.Variant][meta.Target].ContainersByPod[meta.Pod]
		updated := sets.NewString(current...)
		updated.Insert(meta.Container)
		s.indexedMetadata[meta.Org][meta.Repo][meta.Branch][meta.Variant][meta.Target].ContainersByPod[meta.Pod] = updated.List()
		s.lock.Unlock()

		overall := circonusllhist.New()
		var members []*circonusllhist.Histogram
		for _, fingerprint := range fingerprints {
			overall.Merge(data.Data[fingerprint])
			members = append(members, data.Data[fingerprint])
		}
		s.lock.Lock()
		if _, exists := s.byMetaData[meta]; !exists {
			s.byMetaData[meta] = map[corev1.ResourceName]dataForDisplay{}
		}
		s.byMetaData[meta][metric] = dataForDisplay{
			Cutoff:     overall.ValueAtQuantile(quantile),
			Merged:     overall,
			Histograms: members,
		}
		s.lock.Unlock()
	}
	s.logger.Debug("Finished digesting new data.")
}
