package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/retry"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	crcontrollerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openshift/ci-tools/pkg/api/vault"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/vaultclient"
)

// https://github.com/openshift/ci-tools/blob/7af2e075f381ecae1562d1406bad2c86a23e72a3/vendor/k8s.io/api/core/v1/types.go#L5748-L5749
const secretKeyValidationRegexString = `^[a-zA-Z0-9\.\-_]+$`

var secretKeyValidationRegex = regexp.MustCompile(secretKeyValidationRegexString)

type kvUpdateTransport struct {
	kvMountPath string
	upstream    http.RoundTripper
	kubeClients func() map[string]ctrlruntimeclient.Client
	// If enabled, the roundtripper will wait for secret
	// sync to complete. Should only be enabled in tests.
	synchronousSecretSync bool

	privilegedVaultClient *vaultclient.VaultClient
	// existingSecretKeysByNamespaceName is used in the key validation.
	existingSecretKeysByNamespaceName     map[types.NamespacedName]sets.String
	existingSecretKeysByNamespaceNameLock sync.RWMutex
	// existingSecretKeysByVaultSecretName is used as an index for updating
	// the key cache (existingSecretKeysByNamespaceName) when Vault entries
	// get updated/deleted.
	existingSecretKeysByVaultSecretName map[string][]namespacedNameKey
}

func (k *kvUpdateTransport) initialize() {
	for err := k.populateKeyCache(context.Background()); err != nil; {
		logrus.WithError(err).Error("failed to populate key cache")
	}
}

type namespacedNameKey struct {
	name types.NamespacedName
	key  string
}

func namespacedNameKeySliceContains(haystack []namespacedNameKey, needle namespacedNameKey) bool {
	for _, hay := range haystack {
		if reflect.DeepEqual(hay, needle) {
			return true
		}
	}

	return false
}

func (k *kvUpdateTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	l := logrus.WithFields(logrus.Fields{
		"method": r.Method,
		"path":   r.URL.Path,
	})
	l.Debug("Received request")
	if (r.Method != http.MethodPut && r.Method != http.MethodPost && r.Method != http.MethodPatch && r.Method != http.MethodDelete) || !strings.HasPrefix(r.URL.Path, "/v1/"+k.kvMountPath) {
		return k.upstream.RoundTrip(r)
	}
	if r.Method == http.MethodDelete {
		resp, err := k.upstream.RoundTrip(r)
		if err != nil || resp.StatusCode < 200 || resp.StatusCode > 299 {
			return resp, err
		}
		k.updateKeyCacheForSecret(strings.TrimPrefix(r.URL.Path, "/v1/"), nil)
		return resp, err
	}
	l.Debug("Checking if kv keys in request are valid")

	requestBodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logrus.WithError(err).Error("failed to read request body")
		return newResponse(http.StatusInternalServerError, r, "failed to read request body"), nil
	}

	var body simpleKVUpdateRequestBody
	if err := json.Unmarshal(requestBodyBytes, &body); err != nil {
		logrus.WithError(err).WithField("raw-body", string(requestBodyBytes)).Error("failed to unmarshal request body")
		return newResponse(http.StatusInternalServerError, r, "failed to deserialize request body"), nil
	}

	var errs []string
	for key, value := range body.Data {
		if key == vault.SecretSyncTargetNamepaceKey {
			for _, namespace := range strings.Split(value, ",") {
				if valueErrs := validation.IsDNS1123Label(namespace); len(valueErrs) > 0 {
					errs = append(errs, fmt.Sprintf("value of key %s is invalid: %v", key, valueErrs))
				}
			}
			continue
		}
		if key == vault.SecretSyncTargetNameKey {
			if valueErrs := validation.IsDNS1123Label(value); len(valueErrs) > 0 {
				errs = append(errs, fmt.Sprintf("value of key %s is invalid: %v", key, valueErrs))
			}
			continue
		}
		if key == vault.SecretSyncTargetClusterKey {
			continue
		}
		if !secretKeyValidationRegex.MatchString(key) {
			errs = append(errs, fmt.Sprintf("key %s is invalid: must match regex %s", key, secretKeyValidationRegexString))
		}
	}

	if err := steps.ValidateSecretInStep(body.Data[vault.SecretSyncTargetNamepaceKey], body.Data[vault.SecretSyncTargetNameKey]); err != nil {
		errs = append(errs, fmt.Sprintf("secret %s in namespace %s cannot be used in a step: %s", body.Data[vault.SecretSyncTargetNameKey], body.Data[vault.SecretSyncTargetNamepaceKey], err.Error()))
	}

	keyConflictValidationErrs, err := k.validateKeysDontConflict(r.Context(), r.URL.Path, body.Data)
	if err != nil {
		logrus.WithError(err).Error("Failed to validate keys don't conflict")
		errs = append(errs, "secret key validation check failed, please contact @dptp-helpdesk in #forum-testplatform")
	}
	errs = append(errs, keyConflictValidationErrs...)

	if len(errs) > 0 {
		return newResponse(400, r, errs...), nil
	}

	r.Body = io.NopCloser(bytes.NewBuffer(requestBodyBytes))
	response, err := k.upstream.RoundTrip(r)
	if err != nil {
		return response, err
	}

	if k.synchronousSecretSync {
		k.syncSecret(body.Data)
	} else {
		go k.syncSecret(body.Data)
	}
	if response.StatusCode > 199 && response.StatusCode < 300 {
		k.updateKeyCacheForSecret(r.URL.Path, body.Data)

	}
	return response, nil
}

func (k *kvUpdateTransport) kvCacheKeyFromURLPath(urlPath string) string {
	urlPath = strings.TrimPrefix(urlPath, "/v1/")
	// We use the item name as cache key, so we have to remove metadata/data from the path.
	if strings.HasPrefix(urlPath, k.kvMountPath+"/metadata/") {
		urlPath = strings.Replace(urlPath, "metadata/", "", 1)
	}
	if strings.HasPrefix(urlPath, k.kvMountPath+"/data/") {
		urlPath = strings.Replace(urlPath, "data/", "", 1)
	}
	return urlPath
}

func (k *kvUpdateTransport) updateKeyCacheForSecret(path string, item map[string]string) {
	if k.privilegedVaultClient == nil {
		return
	}

	path = k.kvCacheKeyFromURLPath(path)

	k.existingSecretKeysByNamespaceNameLock.Lock()
	defer k.existingSecretKeysByNamespaceNameLock.Unlock()

	// Clear old entries associated with us
	for _, existingEntry := range k.existingSecretKeysByVaultSecretName[path] {
		k.existingSecretKeysByNamespaceName[existingEntry.name].Delete(existingEntry.key)
	}
	delete(k.existingSecretKeysByVaultSecretName, path)

	for _, namespace := range strings.Split(item[vault.SecretSyncTargetNamepaceKey], ",") {
		name := types.NamespacedName{Namespace: namespace, Name: item[vault.SecretSyncTargetNameKey]}
		if name.Namespace == "" || name.Name == "" {
			return
		}

		// Create new entries
		if k.existingSecretKeysByNamespaceName[name] == nil {
			k.existingSecretKeysByNamespaceName[name] = sets.String{}
		}
		for key := range item {
			if key == vault.SecretSyncTargetNamepaceKey || key == vault.SecretSyncTargetNameKey {
				continue
			}
			k.existingSecretKeysByNamespaceName[name].Insert(key)
			k.existingSecretKeysByVaultSecretName[path] = append(k.existingSecretKeysByVaultSecretName[path], namespacedNameKey{name: name, key: key})
		}
	}
}

func (k *kvUpdateTransport) validateKeysDontConflict(ctx context.Context, path string, data map[string]string) (validationErrs []string, err error) {
	if k.privilegedVaultClient == nil {
		return nil, nil
	}

	if err := k.populateKeyCache(ctx); err != nil {
		return nil, err
	}

	k.existingSecretKeysByNamespaceNameLock.RLock()
	defer k.existingSecretKeysByNamespaceNameLock.RUnlock()
	for _, namespace := range strings.Split(data[vault.SecretSyncTargetNamepaceKey], ",") {
		if namespace == "" || data[vault.SecretSyncTargetNameKey] == "" {
			continue
		}

		path = k.kvCacheKeyFromURLPath(path)

		name := types.NamespacedName{Namespace: namespace, Name: data[vault.SecretSyncTargetNameKey]}
		for key := range data {
			if key == vault.SecretSyncTargetNamepaceKey || key == vault.SecretSyncTargetNameKey || key == vault.SecretSyncTargetClusterKey {
				continue
			}
			if k.existingSecretKeysByNamespaceName[name].Has(key) && !namespacedNameKeySliceContains(k.existingSecretKeysByVaultSecretName[path], namespacedNameKey{name: name, key: key}) {
				validationErrs = append(validationErrs, fmt.Sprintf("key %s in secret %s is already claimed", key, name))
			}
		}
	}

	return validationErrs, nil
}

func (k *kvUpdateTransport) populateKeyCache(ctx context.Context) (err error) {
	if k.privilegedVaultClient == nil {
		return nil
	}

	k.existingSecretKeysByNamespaceNameLock.Lock()
	defer k.existingSecretKeysByNamespaceNameLock.Unlock()

	if k.existingSecretKeysByNamespaceName != nil {
		return nil
	}

	k.existingSecretKeysByNamespaceName = map[types.NamespacedName]sets.String{}
	k.existingSecretKeysByVaultSecretName = map[string][]namespacedNameKey{}
	// Clear up the map if we had an error, to avoid caching an incomplete result
	defer func() {
		if err != nil {
			k.existingSecretKeysByNamespaceName = nil
		}
	}()

	everything, err := k.privilegedVaultClient.ListKVRecursively(k.kvMountPath)
	if err != nil {
		return fmt.Errorf("listKVRecursively failed: %w", err)
	}
	// We fetch pretty much all data, use limited concurrency to not get oom killed
	sema := semaphore.NewWeighted(10)
	wg := &sync.WaitGroup{}
	wg.Add(len(everything))

	var fetchErrs []error
	var fetchErrLock sync.Mutex

	// Need a new lock to synchronize across the fetching goroutines
	var existingSecretKeysByNamespaceNameWriteLock sync.Mutex
	for _, path := range everything {
		path := path
		go func() {
			defer wg.Done()
			if err := sema.Acquire(ctx, 1); err != nil {
				fetchErrLock.Lock()
				fetchErrs = append(fetchErrs, err)
				fetchErrLock.Unlock()
				return
			}
			defer sema.Release(1)

			item, err := k.privilegedVaultClient.GetKV(path)
			if err != nil {
				fetchErrLock.Lock()
				fetchErrs = append(fetchErrs, err)
				fetchErrLock.Unlock()
				return
			}

			existingSecretKeysByNamespaceNameWriteLock.Lock()
			defer existingSecretKeysByNamespaceNameWriteLock.Unlock()

			namespaces := strings.Split(item.Data[vault.SecretSyncTargetNamepaceKey], ",")
			for _, namespace := range namespaces {
				name := types.NamespacedName{Namespace: namespace, Name: item.Data[vault.SecretSyncTargetNameKey]}
				if name.Namespace == "" || name.Name == "" {
					continue
				}
				delete(item.Data, vault.SecretSyncTargetNamepaceKey)
				delete(item.Data, vault.SecretSyncTargetNameKey)

				if k.existingSecretKeysByNamespaceName[name] == nil {
					k.existingSecretKeysByNamespaceName[name] = make(sets.String, len(item.Data))
				}
				for key := range item.Data {
					k.existingSecretKeysByNamespaceName[name].Insert(key)
					k.existingSecretKeysByVaultSecretName[path] = append(k.existingSecretKeysByVaultSecretName[path], namespacedNameKey{name: name, key: key})
				}
			}

		}()
	}
	wg.Wait()

	if err := utilerrors.NewAggregate(fetchErrs); err != nil {
		return fmt.Errorf("failed to fetch secrets from vault: %w", err)
	}

	return nil
}

func (k *kvUpdateTransport) syncSecret(data map[string]string) {
	if k.kubeClients == nil || data[vault.SecretSyncTargetNamepaceKey] == "" || data[vault.SecretSyncTargetNameKey] == "" {
		return
	}
	// This is part of a long-running server, so give a gracious timeout
	// to prevent stuck goroutines
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	for cluster, client := range k.kubeClients() {
		if !vault.TargetsCluster(cluster, data) {
			continue
		}

		for _, namespace := range strings.Split(data[vault.SecretSyncTargetNamepaceKey], ",") {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      data[vault.SecretSyncTargetNameKey],
			}}

			// The returned error is always nil, but we have to keep this signature to be able to pass this to CreateOrUpdate
			// nolint:unparam
			mutateFn := func() error {
				if secret.Data == nil {
					secret.Data = map[string][]byte{}
				}
				for k, v := range data {
					if k == vault.SecretSyncTargetNamepaceKey || k == vault.SecretSyncTargetNameKey || k == vault.SecretSyncTargetClusterKey {
						continue
					}
					secret.Data[k] = []byte(v)
				}

				return nil
			}

			var result crcontrollerutil.OperationResult
			var err error
			if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				result, err = crcontrollerutil.CreateOrUpdate(ctx, client, secret, mutateFn)
				return err
			}); err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"cluster":   cluster,
					"namespace": secret.Namespace,
					"name":      secret.Name,
				}).Error("failed to upsert secret")
				continue
			}
			if result != crcontrollerutil.OperationResultNone {
				logrus.WithFields(logrus.Fields{
					"cluster":   cluster,
					"namespace": secret.Namespace,
					"name":      secret.Name,
					"operation": result,
				}).Debug("Upserted secret")
			}
		}
	}
}

func newResponse(statusCode int, req *http.Request, errs ...string) *http.Response {
	var body []byte
	// We have to properly encode this, otherwise the UI just prints an "Error: [object Object]" which is
	// not particularly helpful
	headers := http.Header{}
	if len(errs) > 0 {
		respError := errorResponse{Errors: errs}
		var err error
		body, err = json.Marshal(respError)
		if err != nil {
			// Fall back to just directly putting the errors into the body
			body = []byte(strings.Join(errs, "\n"))
			logrus.WithError(err).Error("failed to serialize vault error response")
		} else {
			headers.Set("Content-Type", "application/json")
		}
	}
	return &http.Response{
		StatusCode:    statusCode,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Body:          io.NopCloser(bytes.NewBuffer(body)),
		ContentLength: int64(len(body)),
		Request:       req,
		Header:        headers,
	}
}

// errorResponse is the raw structure of errors when they're returned by the
// HTTP API.
// This is copied from github.com/hashicorp/vault/api/response.go because
// they don't have json tags there, resulting in an upper-cases json field
// in the response which makes the UI just diplay `Error: [object Object]`
type errorResponse struct {
	Errors []string `json:"errors"`
}

type simpleKVUpdateRequestBody struct {
	Data map[string]string `json:"data"`
}
