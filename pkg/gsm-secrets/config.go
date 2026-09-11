package gsmsecrets

import (
	"fmt"
	"sort"

	"cloud.google.com/go/iam/apiv1/iampb"
	"google.golang.org/genproto/googleapis/type/expr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/group"
)

// GetDesiredState parses the configuration file and builds the desired state specifications.
// Returns desired service account specs, secret specs, IAM binding specs, the set of active
// collections, and the collections each claimed group's e-mail owns.
func GetDesiredState(configFile string, config Config) ([]ServiceAccountInfo, map[string]GCPSecret, []*iampb.Binding, map[string]bool, map[string][]string, error) {
	groupConfig, err := group.LoadConfig(configFile)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("failed to load file: %w", err)
	}

	var groupNames []string
	for name := range groupConfig.Groups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)

	claimedCollections := sets.New[string]()
	unclaimedCollections := sets.New[string]()
	collectionsWithSA := sets.New[string]()
	for _, name := range groupNames {
		groupCfg := groupConfig.Groups[name]
		if groupCfg.Unclaimed {
			unclaimedCollections.Insert(groupCfg.SecretCollections...)
			continue
		}
		claimedCollections.Insert(groupCfg.SecretCollections...)
		collectionsWithSA.Insert(groupCfg.UpdaterServiceAccounts...)
	}

	var desiredSAs []ServiceAccountInfo
	desiredSecrets := make(map[string]GCPSecret)
	desiredCollections := make(map[string]bool)
	var desiredIAMBindings []*iampb.Binding

	// Keep every referenced collection alive so its migrated data secrets are not deleted.
	// Unclaimed ones get an index secret too, so that listing them works and so that a later
	// claim does not start from a missing index.
	for _, collection := range sets.List(unclaimedCollections) {
		desiredSecrets[GetIndexSecretName(collection)] = GCPSecret{
			Name:       GetIndexSecretName(collection),
			Type:       SecretTypeIndex,
			Collection: collection,
		}
	}
	for _, collection := range claimedCollections.Union(unclaimedCollections).UnsortedList() {
		desiredCollections[collection] = true
	}

	for _, collection := range sets.List(claimedCollections) {
		desiredSecrets[GetIndexSecretName(collection)] = GCPSecret{
			Name:       GetIndexSecretName(collection),
			Type:       SecretTypeIndex,
			Collection: collection,
		}

		if !collectionsWithSA.Has(collection) {
			continue
		}

		desiredSAs = append(desiredSAs, ServiceAccountInfo{
			Email:       GetUpdaterSAEmail(collection, config),
			DisplayName: GetUpdaterSADisplayName(collection),
			ID:          GetUpdaterSAId(collection),
			Collection:  collection,
			Description: GetUpdaterSADescription(collection),
		})
		desiredSecrets[GetUpdaterSASecretName(collection)] = GCPSecret{
			Name:       GetUpdaterSASecretName(collection),
			Type:       SecretTypeSA,
			Collection: collection,
		}

		// The service account gets its own bindings rather than joining the owning group's, so
		// its access stays scoped to exactly one collection.
		saMembers := []string{fmt.Sprintf("serviceAccount:%s", GetUpdaterSAEmail(collection, config))}
		desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
			Role:    config.GetSecretAccessorRole(),
			Members: saMembers,
			Condition: &expr.Expr{
				Expression: BuildSecretAccessorRoleConditionExpression(collection),
				Title:      GetSecretsViewerConditionTitle(collection),
			},
		})
		desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
			Role:    config.GetSecretUpdaterRole(),
			Members: saMembers,
			Condition: &expr.Expr{
				Expression: BuildSecretUpdaterRoleConditionExpression(collection),
				Title:      GetSecretsUpdaterConditionTitle(collection),
			},
		})
	}

	// Per claimed group: one viewer and one updater binding covering all of the group's
	// collections, however many it owns.
	groupCollections := make(map[string][]string)
	for _, name := range groupNames {
		groupCfg := groupConfig.Groups[name]
		if groupCfg.Unclaimed || len(groupCfg.SecretCollections) == 0 {
			continue
		}
		email := fmt.Sprintf("%s@redhat.com", name)
		groupMembers := []string{fmt.Sprintf("group:%s", email)}

		collections := make([]string, len(groupCfg.SecretCollections))
		copy(collections, groupCfg.SecretCollections)
		sort.Strings(collections)
		groupCollections[email] = collections

		desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
			Role:    config.GetSecretAccessorRole(),
			Members: groupMembers,
			Condition: &expr.Expr{
				Expression: BuildSecretAccessorRoleConditionExpressionForCollections(collections),
				Title:      GetSecretsViewerGroupConditionTitle(name),
			},
		})
		desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
			Role:    config.GetSecretUpdaterRole(),
			Members: groupMembers,
			Condition: &expr.Expr{
				Expression: BuildSecretUpdaterRoleConditionExpressionForCollections(collections),
				Title:      GetSecretsUpdaterGroupConditionTitle(name),
			},
		})
	}

	return desiredSAs, desiredSecrets, desiredIAMBindings, desiredCollections, groupCollections, nil
}
