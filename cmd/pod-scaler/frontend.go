package main

import (
	"crypto/sha256"
	"embed"
	_ "embed"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io/fs"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openhistogram/circonusllhist"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/simplifypath"

	"github.com/openshift/ci-tools/pkg/api"
	pod_scaler "github.com/openshift/ci-tools/pkg/pod-scaler"
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
	BuildQuery     = "build"
	ContainerQuery = "container"
)

// metadataQueryMapping defines how we expose a metadata entry to the user -
// in many cases we can know that a specific metadata item only needs some
// subset of fields to fully qualify it, so we will not expose all of them
// to the user on the UI. Furthermore, we need to map those fields to query
// parameters and expose that information to the UI as well.
type metadataQueryMapping struct {
	// matches determines if the given metadata matches this mapping
	matches func(*pod_scaler.FullMetadata) bool

	// preProcess acts on the metadata before the mapping is sent to the UI
	preProcess func(*pod_scaler.FullMetadata)
	// fields define how metadata is mapped to a query
	fields []*fieldMapping
	// postProcess acts on the metadata after the mapping is read from a request
	postProcess func(*pod_scaler.FullMetadata)
}

type fieldMapping struct {
	// query is the request query that should hold this field
	query string
	// field is the metadata field the query should be inserted into
	field func(*pod_scaler.FullMetadata) *string
	// optional defines if the query is required
	optional bool
}

// metadataFromQuery uses the mapping to extract metadata from a query
func (m *metadataQueryMapping) metadataFromQuery(w http.ResponseWriter, r *http.Request) (pod_scaler.FullMetadata, error) {
	meta := pod_scaler.FullMetadata{
		Metadata: api.Metadata{},
	}
	for _, entry := range m.fields {
		value := r.URL.Query().Get(entry.query)
		if value == "" && !entry.optional {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "%s query missing", entry.query)
			return pod_scaler.FullMetadata{}, fmt.Errorf("missing query %q", entry.query)
		}
		into := entry.field(&meta)
		*into = value
	}
	if m.postProcess != nil {
		m.postProcess(&meta)
	}
	return meta, nil
}

func (m *metadataQueryMapping) nodesFromMeta(metadata *pod_scaler.FullMetadata) []*IndexNodeMeta {
	var nodes []*IndexNodeMeta
	meta := *metadata // do not mutate the passed metadata
	if m.preProcess != nil {
		m.preProcess(&meta)
	}
	for _, field := range m.fields {
		f := field.field(&meta)
		if (f == nil || *f == "") && !field.optional {
			// this record won't round-trip since we require a field we don't have
			return nil
		}
		nodes = append(nodes, &IndexNodeMeta{
			Name:  *f,
			Field: field.query,
		})
	}
	return nodes
}

func endpoints() map[string]metadataQueryMapping {
	return map[string]metadataQueryMapping{
		"steps": {
			matches: func(meta *pod_scaler.FullMetadata) bool {
				return meta.Step != ""
			},
			fields: []*fieldMapping{
				{query: OrgQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Org }},
				{query: RepoQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Repo }},
				{query: BranchQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Branch }},
				{query: VariantQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Variant }, optional: true},
				{query: TargetQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Target }},
				{query: StepQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Step }},
				{query: ContainerQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Container }},
			},
			postProcess: func(meta *pod_scaler.FullMetadata) {
				meta.Pod = fmt.Sprintf("%s-%s", meta.Target, meta.Step)
			},
		},
		"builds": {
			matches: func(meta *pod_scaler.FullMetadata) bool {
				return meta.Target == "" && strings.HasSuffix(meta.Pod, "-build")
			},
			preProcess: func(meta *pod_scaler.FullMetadata) {
				meta.Pod = strings.TrimSuffix(meta.Pod, "-build")
			},
			fields: []*fieldMapping{
				{query: OrgQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Org }},
				{query: RepoQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Repo }},
				{query: BranchQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Branch }},
				{query: VariantQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Variant }, optional: true},
				{query: BuildQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Pod }},
				{query: ContainerQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Container }},
			},
			postProcess: func(meta *pod_scaler.FullMetadata) {
				meta.Pod += "-build"
			},
		},
		"pods": {
			matches: func(meta *pod_scaler.FullMetadata) bool {
				return meta.Target != "" && meta.Step == ""
			},
			fields: []*fieldMapping{
				{query: OrgQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Org }},
				{query: RepoQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Repo }},
				{query: BranchQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Branch }},
				{query: VariantQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Variant }, optional: true},
				{query: TargetQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Target }},
				{query: ContainerQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Container }},
			},
			postProcess: func(meta *pod_scaler.FullMetadata) {
				meta.Pod = meta.Target
			},
		},
		"rpms": {
			matches: func(meta *pod_scaler.FullMetadata) bool {
				return meta.Container == "rpm-repo"
			},
			fields: []*fieldMapping{
				{query: OrgQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Org }},
				{query: RepoQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Repo }},
				{query: BranchQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Branch }},
				{query: VariantQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Metadata.Variant }, optional: true},
			},
			postProcess: func(meta *pod_scaler.FullMetadata) {
				meta.Container += "rpm-repo"
			},
		},
		"prowjobs": {
			matches: func(meta *pod_scaler.FullMetadata) bool {
				return meta.Org == ""
			},
			fields: []*fieldMapping{
				{query: TargetQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Target }},
				{query: ContainerQuery, field: func(meta *pod_scaler.FullMetadata) *string { return &meta.Container }},
			},
		},
	}
}

var (
	uiMetrics = metrics.NewMetrics("pod_scaler_ui")

	//go:embed frontend/dist
	static embed.FS
)

func serveUI(port, healthPort int, dataDir string, loaders map[string][]*cacheReloader) {
	logger := logrus.WithField("component", "pod-scaler frontend")
	server := &frontendServer{
		logger:   logger,
		lock:     sync.RWMutex{},
		mappings: endpoints(),
		indices:  map[string][]*IndexNode{},
		dataDir:  dataDir,
	}
	health := pjutil.NewHealthOnPort(healthPort)
	digestAll(loaders, map[string]digester{
		MetricNameCPUUsage:         server.digestCPU,
		MetricNameMemoryWorkingSet: server.digestMemory,
	}, health, logger)

	var nodes []simplifypath.Node
	for name := range server.mappings {
		nodes = append(nodes, l(name))
	}

	simplifier := simplifypath.NewSimplifier(l("", // shadow element mimicing the root
		l(""), // actual UI
		l("api",
			l("data",
				nodes...,
			),
			l("indicies",
				nodes...,
			),
		),
	))
	handler := metrics.TraceHandler(simplifier, uiMetrics.HTTPRequestDuration, uiMetrics.HTTPResponseSize)
	mux := http.NewServeMux()
	stripped, err := fs.Sub(static, "frontend/dist")
	if err != nil {
		logger.WithError(err).Fatal("Could not prefix static content.")
	}
	index, err := stripped.Open("index.html")
	if err != nil {
		logger.WithError(err).Fatal("Could not find index.html in static content.")
	}
	indexBytes, err := ioutil.ReadAll(index)
	if err != nil {
		logger.WithError(err).Fatal("Could not read index.html.")
	}
	if err := index.Close(); err != nil {
		logger.WithError(err).Fatal("Could not close index.html.")
	}
	mux.HandleFunc("/", handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(indexBytes); err != nil {
			logrus.WithError(err).Warn("Could not serve index.html.")
		}
	})).ServeHTTP)
	mux.HandleFunc("/static/", handler(http.StripPrefix("/static/", http.FileServer(http.FS(stripped)))).ServeHTTP)
	for name := range server.mappings {
		mux.HandleFunc(fmt.Sprintf("/api/data/%s", name), handler(server.getData(name)).ServeHTTP)
		mux.HandleFunc(fmt.Sprintf("/api/indices/%s", name), handler(server.getIndex(name)).ServeHTTP)
	}
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: mux}
	interrupts.ListenAndServe(httpServer, 5*time.Second)
	logger.Debug("Ready to serve HTTP requests.")
}

type IndexNode struct {
	*IndexNodeMeta `json:",inline"`
	Children       []*IndexNode `json:"children"`
}

type IndexNodeMeta struct {
	Name  string `json:"name"`
	Field string `json:"field"`
}

// insert uses a DFS to find the correct spot to add the child
func insert(roots []*IndexNode, chain []*IndexNodeMeta) *IndexNode {
	for _, root := range roots {
		if root.Name == chain[0].Name {
			insertInto(root, chain[1:])
			return nil
		}
	}
	newChild := IndexNode{IndexNodeMeta: chain[0]}
	insertInto(&newChild, chain[1:])
	return &newChild
}

func insertInto(root *IndexNode, chain []*IndexNodeMeta) {
	if len(chain) == 0 {
		return
	}
	for _, child := range root.Children {
		if child.Name == chain[0].Name {
			insertInto(child, chain[1:])
			return
		}
	}
	newChild := IndexNode{IndexNodeMeta: chain[0]}
	root.Children = append(root.Children, &newChild)
	insertInto(&newChild, chain[1:])
}

func sortChildren(root *IndexNode) {
	for _, child := range root.Children {
		sortChildren(child)
	}
	sort.Slice(root.Children, func(i, j int) bool {
		return root.Children[i].Name < root.Children[j].Name
	})
}

type frontendServer struct {
	logger *logrus.Entry
	lock   sync.RWMutex

	// mappings define how we expose metadata indices
	mappings map[string]metadataQueryMapping

	// indices hold identifiers for classes of metadata
	indices map[string][]*IndexNode

	// dataDir is where we hold sharded data by metadata identifier
	dataDir string
}

// dataForDisplay caches precomputed values for displaying data
type dataForDisplay struct {
	Cutoff     float64                     `json:"cutoff"`
	LowerBound float64                     `json:"lower_bound"`
	Merged     *circonusllhist.Histogram   `json:"merged"`
	Histograms []*circonusllhist.Histogram `json:"histograms"`
}

func (s *frontendServer) getIndex(index string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.lock.RLock()
		data := s.indices[index]
		s.lock.RUnlock()
		raw, err := json.Marshal(data)
		if err != nil {
			metrics.RecordError("failed to marshal index data", uiMetrics.ErrorRate)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to marshal index data to JSON: %v", err)
			s.logger.WithError(err).Errorf("Failed to marshal index data to JSON.")
			return
		}
		if _, err := w.Write(raw); err != nil {
			s.logger.WithError(err).Error("failed to write index response")
		}
	}
}

func (s *frontendServer) getData(index string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return
		}
		mapping := s.mappings[index]
		meta, err := mapping.metadataFromQuery(w, r)
		if err != nil {
			metrics.RecordError("invalid query", uiMetrics.ErrorRate)
			return
		}
		logger := logrus.WithFields(meta.LogFields())
		s.lock.RLock()
		data, found, err := s.getDatum(meta)
		s.lock.RUnlock()
		if !found {
			metrics.RecordError("data not found", uiMetrics.ErrorRate)
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "no data available")
			logger.Warning("No data found.")
			return
		}
		if err != nil {
			metrics.RecordError("data read error", uiMetrics.ErrorRate)
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "error reading data")
			logger.WithError(err).Warning("Failed to read data.")
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

func hashed(meta pod_scaler.FullMetadata) (string, error) {
	raw, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("could not marshal metadata: %w", err)
	}
	hash := sha256.New()
	if _, err := hash.Write(raw); err != nil {
		return "", fmt.Errorf("could not hash metadata: %w", err)
	}
	return base32.StdEncoding.EncodeToString(hash.Sum(nil)), nil
}

func (s *frontendServer) getDatum(meta pod_scaler.FullMetadata) (map[corev1.ResourceName]dataForDisplay, bool, error) {
	hash, err := hashed(meta)
	if err != nil {
		return nil, false, fmt.Errorf("could not determine hash for meta: %w", err)
	}
	subDir := filepath.Join(s.dataDir, hash)
	if _, err := os.Stat(subDir); os.IsNotExist(err) {
		return nil, false, nil
	}
	datum := map[corev1.ResourceName]dataForDisplay{}
	if err := filepath.Walk(subDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		extension := filepath.Ext(info.Name())
		filename := info.Name()[0 : len(info.Name())-len(extension)]
		if extension != ".json" {
			return nil
		}
		raw, err := ioutil.ReadFile(path)
		if err != nil {
			return fmt.Errorf("could not read file: %w", err)
		}
		var subDatum dataForDisplay
		if err := json.Unmarshal(raw, &subDatum); err != nil {
			return fmt.Errorf("could not unmarshal file: %w", err)
		}
		datum[corev1.ResourceName(filename)] = subDatum
		return nil
	}); err != nil {
		return nil, true, fmt.Errorf("failed to read data: %w", err)
	}
	return datum, true, nil
}

func (s *frontendServer) setDatum(meta pod_scaler.FullMetadata, resource corev1.ResourceName, datum dataForDisplay) error {
	hash, err := hashed(meta)
	if err != nil {
		return fmt.Errorf("could not determine hash for meta: %w", err)
	}
	raw, err := json.Marshal(datum)
	if err != nil {
		return fmt.Errorf("could not marshal datum: %w", err)
	}
	subDir := filepath.Join(s.dataDir, hash)
	if err := os.MkdirAll(subDir, 0777); err != nil {
		return fmt.Errorf("could not create directory: %w", err)
	}
	return ioutil.WriteFile(filepath.Join(subDir, fmt.Sprintf("%s.json", string(resource))), raw, 0777)
}

func (s *frontendServer) digestCPU(data *pod_scaler.CachedQuery) {
	s.logger.Debugf("Digesting new CPU consumption metrics.")
	s.digestData(data, corev1.ResourceCPU, cpuRequestQuantile)
}

func (s *frontendServer) digestMemory(data *pod_scaler.CachedQuery) {
	s.logger.Debugf("Digesting new Memory consumption metrics.")
	s.digestData(data, corev1.ResourceMemory, memRequestQuantile)
}

func (s *frontendServer) digestData(data *pod_scaler.CachedQuery, metric corev1.ResourceName, quantile float64) {
	s.logger.Debugf("Digesting %d identifiers.", len(data.DataByMetaData))
	for meta, fingerprints := range data.DataByMetaData {
		s.lock.Lock()
		for name, mapping := range s.mappings {
			if !mapping.matches(&meta) {
				continue
			}
			nodes := mapping.nodesFromMeta(&meta)
			if nodes == nil {
				continue
			}
			root := insert(s.indices[name], nodes)
			if root != nil {
				s.indices[name] = append(s.indices[name], root)
			}
		}

		overall := circonusllhist.New()
		var members []*circonusllhist.Histogram
		for _, fingerprint := range fingerprints {
			overall.Merge(data.Data[fingerprint].Histogram())
			members = append(members, data.Data[fingerprint].Histogram())
		}
		if err := s.setDatum(meta, metric, dataForDisplay{
			Cutoff:     overall.ValueAtQuantile(quantile),
			LowerBound: overall.ValueAtQuantile(.001),
			Merged:     overall,
			Histograms: members,
		}); err != nil {
			s.logger.WithError(err).Error("Could not record data.")
		}
		s.lock.Unlock()
	}

	s.lock.Lock()
	for index := range s.indices {
		for _, root := range s.indices[index] {
			sortChildren(root)
		}
		sort.Slice(s.indices[index], func(i, j int) bool {
			return s.indices[index][i].Name < s.indices[index][j].Name
		})
	}
	s.lock.Unlock()
	s.logger.Debug("Finished digesting new data.")
}
