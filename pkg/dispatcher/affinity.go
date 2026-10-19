package dispatcher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// PlacementAffinity preserves a shared namespace's host until an explicit safe
// release protocol exists. Bindings have no automatic expiry.
type PlacementAffinity struct {
	ID      string `json:"id"`
	Cluster string `json:"cluster"`
}

// affinityBinding records provider identity independently of mutable inventory.
type affinityBinding struct {
	Affinity PlacementAffinity `json:"affinity"`
	Provider string            `json:"provider"`
}

// affinityState retains aliases and deleted jobs so configuration churn cannot
// silently release a namespace that may still contain reusable builds.
type affinityState struct {
	Version  int                        `json:"version"`
	Bindings map[string]affinityBinding `json:"bindings"`
	Groups   map[string]string          `json:"groups"`
	Jobs     map[string]ProwJobData     `json:"jobs"`
}

// AffinityStore durably freezes baseline hosts before any scheduling publication.
type AffinityStore struct {
	mu    sync.Mutex
	path  string
	state affinityState
}

// OpenAffinityStore loads the opt-in freeze store. A missing path starts empty;
// malformed existing state fails closed and never becomes an empty store.
func OpenAffinityStore(path string) (*AffinityStore, error) {
	if path == "" {
		return nil, errors.New("affinity store path is empty")
	}
	store := &AffinityStore{path: path, state: affinityState{Version: 1, Bindings: make(map[string]affinityBinding), Groups: make(map[string]string), Jobs: make(map[string]ProwJobData)}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read affinity store: %w", err)
	}
	store.state = affinityState{}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store.state); err != nil {
		return nil, fmt.Errorf("decode affinity store: %w", err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("affinity store has trailing content")
	}
	if err := validateAffinityState(store.state); err != nil {
		return nil, err
	}
	return store, nil
}

// validateAffinityState rejects dangling aliases and incomplete persisted bindings.
func validateAffinityState(state affinityState) error {
	if state.Version != 1 || state.Bindings == nil || state.Groups == nil || state.Jobs == nil {
		return errors.New("unsupported or incomplete affinity store")
	}
	for id, binding := range state.Bindings {
		if id == "" || binding.Affinity.ID != id || binding.Affinity.Cluster == "" || binding.Provider == "" {
			return fmt.Errorf("invalid affinity binding %q", id)
		}
	}
	for group, id := range state.Groups {
		if _, exists := state.Bindings[id]; group == "" || !exists {
			return fmt.Errorf("invalid affinity group %q", group)
		}
	}
	for name, job := range state.Jobs {
		binding, exists := state.Bindings[job.Affinity.ID]
		if name == "" || !exists || job.Affinity != binding.Affinity || job.Cluster != binding.Affinity.Cluster {
			return fmt.Errorf("invalid affinity alias %q", name)
		}
	}
	return nil
}

// Enrich returns previous assignments plus durable tombstones. Persisted aliases
// take precedence over an older Gob cache after a crash between the two writes.
func (store *AffinityStore) Enrich(previous map[string]ProwJobData) (map[string]ProwJobData, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := copyBaseline(store.state.Jobs)
	for name, job := range copyBaseline(previous) {
		if stored, exists := store.state.Jobs[name]; exists {
			job.Affinity = stored.Affinity
			job.Cluster = stored.Affinity.Cluster
		} else if job.Affinity != (PlacementAffinity{}) {
			return nil, fmt.Errorf("job %q has affinity absent from durable store", name)
		}
		result[name] = job
	}
	return result, nil
}

// affinityGroupKey separates providers and explicit placement exceptions while
// letting new automatically placed siblings inherit an existing family's host.
func affinityGroupKey(name string, job ProwJobData, inventory ClusterMap) (string, error) {
	group := "group:" + job.Group
	if job.Group == "" {
		group = "job:" + name
	}
	info, exists := inventory[job.Cluster]
	if !exists && job.PlacementKnown && !job.Relocatable && job.Cluster != "" {
		return group + "\x00manual:" + job.Cluster, nil
	}
	if !exists || info.Provider == "" {
		return "", fmt.Errorf("job %q cluster %q has no provider inventory", name, job.Cluster)
	}
	key := group + "\x00" + info.Provider
	if job.PlacementKnown && !job.Relocatable {
		key += "\x00fixed:" + job.Cluster
	}
	return key, nil
}

// validateAffinityPlacement makes changed placement restrictions fail closed.
func validateAffinityPlacement(name string, job ProwJobData, binding affinityBinding, inventory ClusterMap) error {
	if strings.HasPrefix(binding.Provider, "manual:") {
		if !job.PlacementKnown || job.Relocatable || job.Cluster != binding.Affinity.Cluster {
			return fmt.Errorf("job %q conflicts with fixed manual affinity host %q", name, binding.Affinity.Cluster)
		}
		if job.Affinity != (PlacementAffinity{}) && job.Affinity != binding.Affinity {
			return fmt.Errorf("job %q carries conflicting affinity", name)
		}
		return nil
	}
	current, exists := inventory[job.Cluster]
	target, targetExists := inventory[binding.Affinity.Cluster]
	if !exists || !targetExists || current.Provider != binding.Provider || target.Provider != binding.Provider {
		return fmt.Errorf("job %q affinity host %q is incompatible with provider inventory", name, binding.Affinity.Cluster)
	}
	if job.PlacementKnown && !job.Relocatable && job.Cluster != binding.Affinity.Cluster {
		return fmt.Errorf("job %q explicit placement %q conflicts with affinity host %q", name, job.Cluster, binding.Affinity.Cluster)
	}
	if !matchesAllCapabilities(target.Capabilities, job.Capabilities) {
		return fmt.Errorf("job %q affinity host %q lacks required capabilities", name, binding.Affinity.Cluster)
	}
	if job.Affinity != (PlacementAffinity{}) && job.Affinity != binding.Affinity {
		return fmt.Errorf("job %q carries conflicting affinity", name)
	}
	return nil
}

// resolveAffinity uses immutable job aliases before a possibly renamed group key.
func resolveAffinity(state affinityState, name string, job ProwJobData, inventory ClusterMap) (PlacementAffinity, bool, error) {
	key, err := affinityGroupKey(name, job, inventory)
	if err != nil {
		return PlacementAffinity{}, false, err
	}
	id := state.Groups[key]
	if previous, exists := state.Jobs[name]; exists {
		if id != "" && id != previous.Affinity.ID {
			return PlacementAffinity{}, false, fmt.Errorf("job %q collides with another affinity group", name)
		}
		id = previous.Affinity.ID
	}
	if id == "" {
		if job.Affinity != (PlacementAffinity{}) {
			return PlacementAffinity{}, false, fmt.Errorf("job %q has unknown affinity", name)
		}
		return PlacementAffinity{}, false, nil
	}
	binding := state.Bindings[id]
	if err := validateAffinityPlacement(name, job, binding, inventory); err != nil {
		return PlacementAffinity{}, false, err
	}
	return binding.Affinity, true, nil
}

// Resolve finds a compatible existing host without creating or persisting a binding.
func (store *AffinityStore) Resolve(name string, job ProwJobData, inventory ClusterMap) (PlacementAffinity, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return resolveAffinity(store.state, name, job, inventory)
}

// ResolveAll finds existing bindings after registering current job aliases in a
// temporary index. This lets new siblings inherit a concurrently renamed group.
// It never creates bindings or changes the durable store.
func (store *AffinityStore) ResolveAll(current map[string]ProwJobData, inventory ClusterMap) (map[string]PlacementAffinity, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state := cloneAffinityState(store.state)
	names := sortedProwJobNames(current)
	for _, name := range names {
		job := current[name]
		if job.Cluster == "" {
			continue
		}
		if _, exists := state.Jobs[name]; !exists {
			continue
		}
		affinity, _, err := resolveAffinity(state, name, job, inventory)
		if err != nil {
			return nil, err
		}
		if err := recordAffinity(state, name, job, affinity, inventory); err != nil {
			return nil, err
		}
	}
	result := make(map[string]PlacementAffinity)
	for _, name := range names {
		job := current[name]
		if job.Cluster == "" {
			continue
		}
		affinity, bound, err := resolveAffinity(state, name, job, inventory)
		if err != nil {
			return nil, err
		}
		if bound {
			result[name] = affinity
		}
	}
	return result, nil
}

// cloneAffinityState isolates planning from the last successfully persisted state.
func cloneAffinityState(state affinityState) affinityState {
	result := affinityState{Version: state.Version, Bindings: make(map[string]affinityBinding, len(state.Bindings)), Groups: make(map[string]string, len(state.Groups)), Jobs: copyBaseline(state.Jobs)}
	for id, binding := range state.Bindings {
		result.Bindings[id] = binding
	}
	for key, id := range state.Groups {
		result.Groups[key] = id
	}
	return result
}

// recordAffinity keeps renamed group keys as aliases without dropping old keys.
func recordAffinity(state affinityState, name string, job ProwJobData, affinity PlacementAffinity, inventory ClusterMap) error {
	key, err := affinityGroupKey(name, job, inventory)
	if err != nil {
		return err
	}
	if existing := state.Groups[key]; existing != "" && existing != affinity.ID {
		return fmt.Errorf("job %q collides with another affinity group", name)
	}
	state.Groups[key] = affinity.ID
	job.Cluster = affinity.Cluster
	job.Affinity = affinity
	state.Jobs[name] = job
	return nil
}

// Commit binds every final unit before the caller writes Gob state or publishes
// routing decisions. Errors leave the input intact; a replaced store keeps its
// bindings even when the final directory sync reports an error.
func (store *AffinityStore) Commit(final map[string]ProwJobData, inventory ClusterMap) (map[string]ProwJobData, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state := cloneAffinityState(store.state)
	result := copyBaseline(final)
	names := sortedProwJobNames(result)
	// Register existing aliases first so a new sibling can find a renamed group.
	for _, name := range names {
		job := result[name]
		if job.Cluster == "" {
			continue
		}
		if _, exists := state.Jobs[name]; !exists {
			continue
		}
		affinity, _, err := resolveAffinity(state, name, job, inventory)
		if err != nil {
			return nil, err
		}
		if job.Cluster != affinity.Cluster {
			return nil, fmt.Errorf("job %q planned cluster %q conflicts with affinity host %q", name, job.Cluster, affinity.Cluster)
		}
		if err := recordAffinity(state, name, job, affinity, inventory); err != nil {
			return nil, err
		}
	}
	for _, name := range names {
		job := result[name]
		if job.Cluster == "" {
			continue
		}
		affinity, bound, err := resolveAffinity(state, name, job, inventory)
		if err != nil {
			return nil, err
		}
		if !bound {
			key, err := affinityGroupKey(name, job, inventory)
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256([]byte(key))
			affinity = PlacementAffinity{ID: hex.EncodeToString(sum[:]), Cluster: job.Cluster}
			provider := inventory[job.Cluster].Provider
			if provider == "" && job.PlacementKnown && !job.Relocatable {
				provider = "manual:" + job.Cluster
			}
			state.Bindings[affinity.ID] = affinityBinding{Affinity: affinity, Provider: provider}
		}
		if job.Cluster != affinity.Cluster {
			return nil, fmt.Errorf("job %q planned cluster %q conflicts with affinity host %q", name, job.Cluster, affinity.Cluster)
		}
		if err := validateAffinityPlacement(name, job, state.Bindings[affinity.ID], inventory); err != nil {
			return nil, err
		}
		if err := recordAffinity(state, name, job, affinity, inventory); err != nil {
			return nil, err
		}
		job.Affinity = affinity
		result[name] = job
	}
	committed, err := writeAffinityState(store.path, state)
	if committed {
		store.state = state
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// writeAffinityState makes bindings durable before their routing decisions escape.
func writeAffinityState(path string, state affinityState) (bool, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return false, fmt.Errorf("create affinity directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".dispatcher-affinity-*")
	if err != nil {
		return false, fmt.Errorf("create affinity temporary file: %w", err)
	}
	defer os.Remove(temporary.Name())
	if err := json.NewEncoder(temporary).Encode(state); err != nil {
		return false, errors.Join(fmt.Errorf("encode affinity state: %w", err), temporary.Close())
	}
	if err := temporary.Sync(); err != nil {
		return false, errors.Join(fmt.Errorf("sync affinity state: %w", err), temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("close affinity state: %w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return false, fmt.Errorf("replace affinity state: %w", err)
	}
	dir, err := os.Open(directory)
	if err != nil {
		return true, fmt.Errorf("open affinity directory: %w", err)
	}
	return true, errors.Join(dir.Sync(), dir.Close())
}
