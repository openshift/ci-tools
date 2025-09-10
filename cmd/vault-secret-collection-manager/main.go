package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/metrics"
	"sigs.k8s.io/prow/pkg/version"

	"github.com/openshift/ci-tools/pkg/vaultclient"
)

const objectPrefix = "secret-collection-manager-managed"

type option struct {
	// Folder under which to create policies
	kvStorePrefix string
	listenAddr    string
	vaultAddr     string
	vaultToken    string
	vaultRole     string

	authBackendType string
	flagutil.InstrumentationOptions
}

func parseOptions() (*option, error) {
	o := &option{}
	flag.StringVar(&o.kvStorePrefix, "kv-store-prefix", "secret/self-managed", "Vault KV folder under which all policies will get created")
	flag.StringVar(&o.listenAddr, "listen-addr", "127.0.0.1:8080", "The address to listen on")
	flag.StringVar(&o.vaultAddr, "vault-addr", "http://127.0.0.1:8300", "The address under which vault should be reached")
	flag.StringVar(&o.vaultToken, "vault-token", "", "The privileged token to use when communicating with vault, must be able to CRUD policies")
	flag.StringVar(&o.vaultRole, "vault-role", "", "The vault role to use, must be able to CRUD policies. Will be used for kubernetes service account auth.")
	flag.StringVar(&o.authBackendType, "auth-backend-type", "oidc", "The backend type used for user authentication.")
	o.InstrumentationOptions.AddFlags(flag.CommandLine)
	flag.Parse()

	var errs []error
	if o.vaultToken == "" && o.vaultRole == "" {
		errs = append(errs, errors.New("--vault-token or --vault-role is required"))
	}
	if err := o.InstrumentationOptions.Validate(false); err != nil {
		errs = append(errs, err)
	}
	return o, utilerrors.NewAggregate(errs)
}

func main() {
	version.Name = "vault-secret-collection-manager"
	logrusutil.ComponentInit()
	logrus.SetLevel(logrus.DebugLevel)
	o, err := parseOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to get options")
	}

	var privilegedVaultClient *vaultclient.VaultClient
	if o.vaultRole != "" {
		privilegedVaultClient, err = vaultclient.NewFromKubernetesAuth(o.vaultAddr, o.vaultRole)
	} else {
		privilegedVaultClient, err = vaultclient.New(o.vaultAddr, o.vaultToken)
	}
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct vault client")
	}

	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz/ready", func(w http.ResponseWriter, r *http.Request) {
		if privilegedVaultClient.IsCredentialExpired() {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "Vault credential expired")
		}
		fmt.Fprintf(w, "OK")
	})
	healthServer := &http.Server{
		Addr:    ":" + strconv.Itoa(o.InstrumentationOptions.HealthPort),
		Handler: healthMux,
	}
	interrupts.ListenAndServe(healthServer, 0)

	metrics.ExposeMetrics(version.Name, config.PushGateway{}, o.MetricsPort)

	manager, server := server(privilegedVaultClient, o.authBackendType, o.kvStorePrefix, o.listenAddr)
	reconciledPolicies, err := manager.reconcilePolicies()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to reconcile policies")
	}
	if len(reconciledPolicies) > 0 {
		logrus.WithField("reconciled_policies", reconciledPolicies).Info("Successfully reconciled policies")
	}
	interrupts.TickLiteral(func() {
		reconciledPolicies, err := manager.reconcilePolicies()
		if err != nil {
			logrus.WithError(err).Error("Failed to reconcile policies")
		}
		if len(reconciledPolicies) > 0 {
			logrus.WithField("reconciled_policies", reconciledPolicies).Info("Successfully reconciled policies")
		}
	}, time.Hour)
	interrupts.ListenAndServe(server, 5*time.Second)
	interrupts.WaitForGracefulShutdown()
}

func server(privilegedVaultClient *vaultclient.VaultClient, authBackendType, kvStorePrefix, listenAddr string) (*secretCollectionManager, *http.Server) {
	manager := &secretCollectionManager{
		privilegedVaultClient:   privilegedVaultClient,
		kvStorePrefix:           kvStorePrefix,
		kvMetadataPrefix:        vaultclient.InsertMetadataIntoPath(kvStorePrefix),
		kvDataPrefix:            vaultclient.InsertDataIntoPath(kvStorePrefix),
		authAccessorBackendType: authBackendType,
	}

	return manager, &http.Server{Addr: listenAddr, Handler: manager.mux()}
}

func userWrapper(upstream func(l *logrus.Entry, user string, w http.ResponseWriter, r *http.Request, params httprouter.Params)) func(*logrus.Entry, http.ResponseWriter, *http.Request, httprouter.Params) {
	return func(l *logrus.Entry, w http.ResponseWriter, r *http.Request, params httprouter.Params) {
		user := strings.Split(r.Header.Get("X-Forwarded-Email"), "@")[0]
		if user == "" {
			http.Error(w, "No user passed", 400)
			logrus.WithField("X-Forwarded-Email", r.Header.Get("X-Forwarded-Email")).Error("Got request with empty user")
			return
		}
		*l = *l.WithField("user", user)
		upstream(l, user, w, r, params)
	}
}

type secretCollectionManager struct {
	privilegedVaultClient *vaultclient.VaultClient
	kvStorePrefix         string
	kvMetadataPrefix      string
	kvDataPrefix          string
	groupCache            idNameCache
	userCache             idNameCache

	authAccessorBackendType   string
	authAccessorBackendID     string
	authAccessorBackendIDLock sync.RWMutex
}

// idNameCache allows to get the id or the name, using
// the opposing attribute. It assumes that the relationship
// is immmutable.
type idNameCache struct {
	lock  sync.RWMutex
	names map[string]string
	ids   map[string]string
}

func (c *idNameCache) byID(id string) string {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.ids[id]
}

func (c *idNameCache) byName(name string) string {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.names[name]
}

func (c *idNameCache) set(name, id string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.names == nil {
		c.names = map[string]string{}
	}
	if c.ids == nil {
		c.ids = map[string]string{}
	}
	c.names[name] = id
	c.ids[id] = name
}

func (m *secretCollectionManager) mux() *instrumentationWrapper {
	router := newInstrumentedRouter()
	// Do not redirect something like POST secretcollection/ where someone tried to
	// create a nameless secret collection to secretcollection
	router.RedirectTrailingSlash = false
	router.GET("/", simpleLoggingWrapper(redirectHandler("/secretcollection?ui=true")))
	router.GET("/style.css", simpleLoggingWrapper(staticFileHandler(styleCSS, "text/css")))
	router.GET("/index.js", simpleLoggingWrapper(staticFileHandler(indexJS, "text/javascript")))
	router.GET("/healthz", simpleLoggingWrapper(healthHandler))
	router.GET("/secretcollection", loggingWrapper(userWrapper(m.listSecretCollections)))
	router.PUT("/secretcollection/:name", loggingWrapper(userWrapper(m.createSecretCollectionHandler)))
	router.PUT("/secretcollection/:name/members", loggingWrapper(userWrapper(m.updateSecretCollectionMembersHandler)))
	router.DELETE("/secretcollection/:name", loggingWrapper(userWrapper(m.deleteCollectionHandler)))
	router.GET("/users", loggingWrapper(userWrapper(m.usersHandler)))
	return router
}

func healthHandler(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	w.WriteHeader(http.StatusOK)
}

func staticFileHandler(content []byte, mimeType string) httprouter.Handle {
	return func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
		w.Header().Add("Content-Type", mimeType)
		if _, err := w.Write(content); err != nil {
			logrus.WithError(err).Error("failed to write response")
		}
	}
}

func redirectHandler(target string) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		http.Redirect(w, r, target, http.StatusFound)
	}
}

func (m *secretCollectionManager) isUserMemberInSecretCollection(l *logrus.Entry, user, collectionName string) (bool, error) {
	collections, err := m.getCollectionsForUser(l, user)
	if err != nil {
		return false, fmt.Errorf("failed to get sceret collections for user %s: %w", user, err)
	}

	for _, collection := range collections {
		if collection.Name == collectionName {
			return true, nil
		}
	}

	return false, nil
}

func (m *secretCollectionManager) deleteCollectionHandler(l *logrus.Entry, user string, w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	name := params.ByName("name")
	if name == "" {
		http.Error(w, "name url parameter must not be empty", 400)
		return
	}

	isMember, err := m.isUserMemberInSecretCollection(l, user, name)
	if err != nil {
		l.WithError(err).Error("failed to check if user is member for secret collection")
		http.Error(w, fmt.Sprintf("failed to check if user is allowed to delete secret collection. RequestID: %s", l.Data["UID"]), http.StatusInternalServerError)
		return
	}
	if !isMember {
		http.Error(w, fmt.Sprintf("secret collection not found. RequestID: %s", l.Data["UID"]), 404)
		return
	}

	if err := m.deleteCollection(name); err != nil {
		l.WithError(err).Error("Failed to delete colection")
		http.Error(w, fmt.Sprintf("failed to delete secret collection. RequestID: %s", l.Data["UID"]), 500)
	}
}

func (m *secretCollectionManager) deleteCollection(name string) error {
	// First delete the data, then the group to be sure that users retain access until all
	// data is deleted.
	path := m.kvStorePrefix + "/" + name
	allItems, err := m.privilegedVaultClient.ListKVRecursively(path)
	if err != nil {
		return fmt.Errorf("failed to list items below %s: %w", path, err)
	}
	for _, item := range allItems {
		if err := m.privilegedVaultClient.DestroyKVIrreversibly(item); err != nil {
			return fmt.Errorf("failed to delete secret at %s: %w", item, err)
		}
	}

	return m.privilegedVaultClient.DeleteGroupByName(prefixedName(name))
}

func (m *secretCollectionManager) updateSecretCollectionMembersHandler(l *logrus.Entry, user string, w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	name := params.ByName("name")
	if name == "" {
		http.Error(w, "name url parameter must not be empty", 400)
		return
	}

	isMember, err := m.isUserMemberInSecretCollection(l, user, name)
	if err != nil {
		l.WithError(err).Error("failed to check if user is member for secret collection")
		http.Error(w, fmt.Sprintf("failed to check if user is allowed to change secret collection. RequestID: %s", l.Data["UID"]), http.StatusInternalServerError)
		return
	}
	if !isMember {
		http.Error(w, fmt.Sprintf("secret collection not found. RequestID: %s", l.Data["UID"]), 404)
		return
	}

	var body secretCollectionUpdateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		l.WithError(err).Debug("failed to decode request body")
		http.Error(w, fmt.Sprintf(`failed to decode request body: %v, expected format: {"members": ["all", "desired", "members"]}`, err), http.StatusBadRequest)
		return
	}

	if len(body.Members) == 0 {
		http.Error(w, "There must be at least one member", http.StatusBadRequest)
		return
	}

	if err := m.updateSecretCollectionMembers(l, name, body.Members); err != nil {
		logrus.WithError(err).Error("failed to update secret collection members")
		http.Error(w, fmt.Sprintf("error updating secret collection members. RequestID: %s", l.Data["UID"]), 500)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (m *secretCollectionManager) updateSecretCollectionMembers(_ *logrus.Entry, collectionName string, updatedMemberNames []string) error {
	var errs []error
	var updatedMemberIDs []string
	for _, memberName := range updatedMemberNames {
		entity, err := m.userByAliasCached(memberName)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find member %s: %w", memberName, err))
			continue
		}
		updatedMemberIDs = append(updatedMemberIDs, entity.ID)
	}
	if err := utilerrors.NewAggregate(errs); err != nil {
		return fmt.Errorf("failed to validate members: %w", err)
	}

	// This is a tad unsafe in case someone else removed us from this group. Would be great to have preconditions :/
	return m.privilegedVaultClient.UpdateGroupMembers(prefixedName(collectionName), updatedMemberIDs)
}

var alphaNumericRegex = regexp.MustCompile("^[a-z0-9-]+$")

func (m *secretCollectionManager) createSecretCollectionHandler(l *logrus.Entry, user string, w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	name := params.ByName("name")
	if name == "" {
		http.Error(w, "name url parameter must not be empty", 400)
		return
	}

	if !alphaNumericRegex.MatchString(name) {
		http.Error(w, fmt.Sprintf("name %q does not match regex '^[a-z0-9-]+$'", name), 400)
		return
	}

	// Conflict on the group, not the policy to keep idempotency. We create the policy, then the group.
	// Whoever creates the group ends up winning.
	if _, err := m.privilegedVaultClient.GetGroupByName(prefixedName(name)); !vaultclient.IsNotFound(err) {
		if err != nil {
			l.WithError(err).WithField("group_name", prefixedName(name)).Error("failed to get group")
			http.Error(w, fmt.Sprintf("failed to get group. RequestID: %s", l.Data["UID"]), 500)
			return
		}
		http.Error(w, fmt.Sprintf("secret collection %q already exists", name), http.StatusConflict)
		return
	}

	if err := m.createSecretCollection(l, user, name); err != nil {
		logrus.WithError(err).Error("failed to create secret collection")
		http.Error(w, fmt.Sprintf("failed to create secret collection. RequestID: %s", l.Data["UID"]), 500)
	}
}

func (m *secretCollectionManager) createSecretCollection(_ *logrus.Entry, userName, secretCollectionName string) error {
	user, err := m.userByAliasCached(userName)
	if err != nil {
		return fmt.Errorf("failed to get user %s: %w", userName, err)
	}
	policy, err := m.serializedPolicyFor(secretCollectionName)
	if err != nil {
		return fmt.Errorf("failed to construct policy for %s: %w", secretCollectionName, err)
	}
	if err := m.privilegedVaultClient.Sys().PutPolicy(prefixedName(secretCollectionName), policy); err != nil {
		return fmt.Errorf("failed to create policy %s: %w", prefixedName(secretCollectionName), err)
	}

	group := vaultclient.Group{
		Name:            prefixedName(secretCollectionName),
		Policies:        []string{prefixedName(secretCollectionName)},
		MemberEntityIDs: []string{user.ID},
		Metadata:        map[string]string{"created-by-secret-collection-manager": "true"},
	}
	serializedGroup, err := json.Marshal(group)
	if err != nil {
		return fmt.Errorf("failed to marhsal group: %w", err)
	}
	if err := m.privilegedVaultClient.Put("identity/group", serializedGroup); err != nil {
		return fmt.Errorf("failed to create group %s: %w", prefixedName(secretCollectionName), err)
	}

	// Create a placeholder file with name and contents greater than a length of 3 to
	// bypass censoring plugin's rules and allow people to see the secret collection in the vault UI.
	indexFileLocation := strings.Replace(m.kvDataPrefix, "/data", "", 1) + "/" + secretCollectionName + "/placeholder"
	if err := m.privilegedVaultClient.UpsertKV(indexFileLocation, map[string]string{"placeholder": "Make entry visible from the webconsole"}); err != nil {
		return fmt.Errorf("failed to create %s: %w", indexFileLocation, err)
	}

	return nil
}

func (m *secretCollectionManager) serializedPolicyFor(name string) (string, error) {
	policy := managedVaultPolicy{Path: map[string]managedVaultPolicyCapabilityList{
		m.kvMetadataPrefix + "/" + name + "/*": {Capabilities: []string{"list", "delete"}},
		m.kvDataPrefix + "/" + name + "/*":     {Capabilities: []string{"create", "update", "read"}},
	}}
	serialized, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("failed to serialize policy: %w", err)
	}

	return string(serialized), nil
}

func prefixedName(name string) string {
	return objectPrefix + "-" + name
}

func nameFromPrefixedName(name string) string {
	return strings.TrimPrefix(name, objectPrefix+"-")
}

func (m *secretCollectionManager) listSecretCollections(l *logrus.Entry, user string, w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	collections, err := m.getCollectionsForUser(l, user)
	if err != nil {
		l.WithError(err).Error("failed to get collections")
		http.Error(w, fmt.Sprintf("failed to get secret collections. RequestID: %s", l.Data["UID"]), 500)
		return
	}

	sort.Slice(collections, func(i, j int) bool {
		return collections[i].Name < collections[j].Name
	})

	serialized, err := json.Marshal(collections)
	if err != nil {
		l.WithError(err).Error("failed to serialize")
		http.Error(w, fmt.Sprintf("failed to serialize. RequestID: %s", l.Data["UID"]), 500)
		return
	}

	if r.URL.Query().Get("ui") == "true" {
		if err := indexTemplate.Execute(w, string(serialized)); err != nil {
			l.WithError(err).Error("failed to execute template response")
		}
	} else if len(collections) > 0 {
		if _, err := w.Write(serialized); err != nil {
			l.WithError(err).Error("failed to write response")
		}
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func (m *secretCollectionManager) getAuthBackendAccessorID() (string, error) {
	var id string
	m.authAccessorBackendIDLock.RLock()
	id = m.authAccessorBackendID
	m.authAccessorBackendIDLock.RUnlock()
	if id != "" {
		return id, nil
	}

	m.authAccessorBackendIDLock.Lock()
	defer m.authAccessorBackendIDLock.Unlock()
	if m.authAccessorBackendID != "" {
		return m.authAccessorBackendID, nil
	}

	authMounts, err := m.privilegedVaultClient.ListAuthMounts()
	if err != nil {
		return "", fmt.Errorf("failed to list auth mounts: %w", err)
	}
	for _, authMount := range authMounts {
		if authMount.Type == m.authAccessorBackendType {
			m.authAccessorBackendID = authMount.Accessor
			break
		}
	}
	if m.authAccessorBackendID == "" {
		return "", fmt.Errorf("couldn't find auth mount for type %s", m.authAccessorBackendType)
	}

	return m.authAccessorBackendID, nil
}

func (m *secretCollectionManager) createUser(userName string) (*vaultclient.Entity, error) {
	authBackendAccessorId, err := m.getAuthBackendAccessorID()
	if err != nil {
		return nil, fmt.Errorf("failed to get auth backend accessor: %w", err)
	}
	user, err := m.privilegedVaultClient.CreateIdentity(userName, []string{"default"})
	if err != nil {
		return nil, fmt.Errorf("failed to create identity for %s: %w", userName, err)
	}
	if err := m.privilegedVaultClient.CreateIdentityAlias(userName, user.ID, authBackendAccessorId); err != nil {
		return nil, fmt.Errorf("failed to create alias for user %s: %w", userName, err)
	}

	return user, nil
}

// getCollectionsForUser returns all collections for a given user. It will also create the user if it doesn't exist yet.
func (m *secretCollectionManager) getCollectionsForUser(l *logrus.Entry, userName string) ([]secretCollection, error) {
	user, err := m.userByAliasCached(userName)
	if err != nil {
		if !vaultclient.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get user %s: %w", userName, err)
		}
		user, err = m.createUser(userName)
		if err != nil {
			return nil, fmt.Errorf("failed to create user %s: %w", userName, err)
		}
		l.Info("Created user in Vault")
	}

	var groupNames []string
	for _, groupID := range user.GroupIDs {
		name, err := m.groupNameFromId(groupID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve name for group id %s: %w", groupID, err)
		}
		if strings.HasPrefix(name, objectPrefix) {
			groupNames = append(groupNames, name)
		}
	}

	var collections []secretCollection
	var errs []error
	for _, groupName := range groupNames {
		collection, err := m.getCollectionsFromGroupName(groupName)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		collections = append(collections, *collection)
	}

	return collections, utilerrors.NewAggregate(errs)
}

func (m *secretCollectionManager) groupNameFromId(id string) (string, error) {
	if cached := m.groupCache.byID(id); cached != "" {
		return cached, nil
	}

	result, err := m.privilegedVaultClient.GetGroupByID(id)
	if err != nil {
		return "", fmt.Errorf("failed to get group %s by id: %w", id, err)
	}

	m.groupCache.set(result.Name, result.ID)
	return result.Name, nil
}

func (m *secretCollectionManager) userByAliasCached(alias string) (*vaultclient.Entity, error) {
	if id := m.userCache.byName(alias); id != "" {
		return m.privilegedVaultClient.GetUserByID(id)
	}

	result, err := m.privilegedVaultClient.GetUserFromAliasName(alias)
	if err != nil {
		return nil, fmt.Errorf("failed to get user from alias %s: %w", alias, err)
	}

	m.userCache.set(alias, result.ID)
	return result, nil
}

func (m *secretCollectionManager) userAliasByIDCached(id string) (string, error) {
	if name := m.userCache.byID(id); name != "" {
		return name, nil
	}
	entity, err := m.privilegedVaultClient.GetUserByID(id)
	if err != nil {
		return "", fmt.Errorf("failed to get user %s by id: %w", id, err)
	}
	// An alias is only unique per auth backend, so we need to require entities
	// to have exactly one or introduce filtering by auth backend.
	if n := len(entity.Aliases); n != 1 {
		return "", notExactlyOneEntityForUserError{fmt.Errorf("entity %s doesn't have exactly one but %d aliases", id, n)}
	}
	m.userCache.set(entity.Aliases[0].Name, entity.ID)
	return entity.Aliases[0].Name, nil
}

type notExactlyOneEntityForUserError struct {
	error
}

func (m *secretCollectionManager) getCollectionsFromGroupName(groupName string) (*secretCollection, error) {
	group, err := m.privilegedVaultClient.GetGroupByName(groupName)
	if err != nil {
		return nil, fmt.Errorf("failed to get group %s: %w", groupName, err)
	}

	if n := len(group.Policies); n != 1 {
		return nil, fmt.Errorf("group %s didn't have exactly one but %d policies attached", groupName, n)
	}

	policy, err := m.privilegedVaultClient.Sys().GetPolicy(group.Policies[0])
	if err != nil {
		return nil, fmt.Errorf("failed to get policy %s: %w", group.Policies[0], err)
	}

	var policyData managedVaultPolicy
	if err := json.Unmarshal([]byte(policy), &policyData); err != nil {
		return nil, fmt.Errorf("failed to unmarhal policy %s: %w", group.Policies[0], err)
	}

	if n := len(policyData.Path); n != 2 {
		return nil, fmt.Errorf("policy %s didn't have two but %d paths", group.Policies[0], n)
	}

	var collection secretCollection
	for path := range policyData.Path {
		if !strings.HasPrefix(path, m.kvMetadataPrefix) && !strings.HasPrefix(path, m.kvDataPrefix) {
			return nil, fmt.Errorf("path %s in policy %s neither had the metadata(%s) nor the data(%s) prefix", path, group.Policies[0], m.kvDataPrefix, m.kvDataPrefix)
		}
		collection.Name = m.collectionNameFromPolicyPath(path)
		collection.Path = strings.Join([]string{m.kvStorePrefix, collection.Name}, "/")
	}

	var memberNames []string
	for _, memberID := range group.MemberEntityIDs {
		name, err := m.userAliasByIDCached(memberID)
		if err != nil {
			return nil, fmt.Errorf("failed to get name for entity %s: %w", memberID, err)
		}
		memberNames = append(memberNames, name)
	}

	collection.Members = memberNames
	return &collection, nil
}

// collectionNameFromPolicyPath strips the metadata/data prefix and the /* suffix from a policy path
func (m *secretCollectionManager) collectionNameFromPolicyPath(policyPath string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(policyPath, m.kvMetadataPrefix+"/"), m.kvDataPrefix+"/"), "/*")
}

func (m *secretCollectionManager) usersHandler(l *logrus.Entry, _ string, w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	entities, err := m.privilegedVaultClient.ListIdentities()
	if err != nil {
		l.WithError(err).Error("Failed to list identities")
		http.Error(w, fmt.Sprintf("failed to list users. RequestID: %s", l.Data["UID"]), 500)
		return
	}

	var userNames []string
	for _, entity := range entities {
		name, err := m.userAliasByIDCached(entity)
		if err != nil && !errors.Is(err, notExactlyOneEntityForUserError{}) {
			l.WithError(err).WithField("userID", entity).Error("Failed to resolve username for id")
		}
		if name != "" {
			userNames = append(userNames, name)
		}
	}

	var serialized []byte
	if len(userNames) > 0 {
		sort.Strings(userNames)
		var err error
		serialized, err = json.Marshal(userNames)
		if err != nil {
			l.WithError(err).Error("Failed to serialize usernames")
			http.Error(w, fmt.Sprintf("failed to list users. RequestID: %s", l.Data["UID"]), 500)
			return
		}
	}
	if _, err := w.Write(serialized); err != nil {
		l.WithError(err).Error("failed to write response")
	}
}

func (m *secretCollectionManager) reconcilePolicies() (updatedPolicies []string, err error) {
	policyNames, err := m.privilegedVaultClient.Sys().ListPolicies()
	if err != nil {
		return nil, fmt.Errorf("failed to list policies: %w", err)
	}
	var errs []error
	for _, policyName := range policyNames {
		if !strings.HasPrefix(policyName, objectPrefix) {
			continue
		}

		policy, err := m.privilegedVaultClient.Sys().GetPolicy(policyName)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get policy %s: %w", policyName, err))
			continue
		}

		expectedPolicy, err := m.serializedPolicyFor(nameFromPrefixedName(policyName))
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to construct expected policy for %s: %w", nameFromPrefixedName(policyName), err))
			continue
		}

		if policy != expectedPolicy {
			if err := m.privilegedVaultClient.Sys().PutPolicy(policyName, expectedPolicy); err != nil {
				errs = append(errs, fmt.Errorf("failed to update outdated policy %s: %w", policyName, err))
				continue
			}

			updatedPolicies = append(updatedPolicies, policyName)
		}
	}

	return updatedPolicies, utilerrors.NewAggregate(errs)
}
