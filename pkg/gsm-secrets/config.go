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
//
// Each collection referenced by a group gets an updater service account, its SA secret, an index
// secret, and service-account-scoped viewer/updater bindings limited to that single collection.
// Each owning group additionally gets its own viewer/updater bindings; a group's collections are
// chunked (at most MaxCollectionsPerGroupBinding per binding) so no single binding exceeds GCP's
// IAM limits on operators per condition and bindings per role+member.
//
// Returns desired service account specs, secret specs, IAM binding specs, and the set of active collections.
func GetDesiredState(configFile string, config Config) ([]ServiceAccountInfo, map[string]GCPSecret, []*iampb.Binding, map[string]bool, error) {
	groupConfig, err := group.LoadConfig(configFile)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to load file: %w", err)
	}

	var groupNames []string
	for name := range groupConfig.Groups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)

	collections := sets.New[string]()
	for _, name := range groupNames {
		collections.Insert(groupConfig.Groups[name].SecretCollections...)
	}

	var desiredSAs []ServiceAccountInfo
	desiredSecrets := make(map[string]GCPSecret)
	desiredCollections := make(map[string]bool)
	var desiredIAMBindings []*iampb.Binding

	for _, collection := range collections.UnsortedList() {
		desiredCollections[collection] = true
	}

	// Per collection: an updater service account, its SA secret, an index secret, and
	// service-account-scoped viewer/updater bindings limited to that single collection. The
	// service account is given its own bindings rather than being grouped with the owning group,
	// so its access stays scoped to exactly one collection.
	for _, collection := range sets.List(collections) {
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
		desiredSecrets[GetIndexSecretName(collection)] = GCPSecret{
			Name:       GetIndexSecretName(collection),
			Type:       SecretTypeIndex,
			Collection: collection,
		}

		saMembers := []string{fmt.Sprintf("serviceAccount:%s", GetUpdaterSAEmail(collection, config))}
		desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
			Role:    config.GetSecretAccessorRole(),
			Members: saMembers,
			Condition: &expr.Expr{
				Expression:  BuildSecretAccessorRoleConditionExpression(collection),
				Title:       GetSecretsViewerConditionTitle(collection),
				Description: GetSecretsViewerConditionDescription(collection),
			},
		})
		desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
			Role:    config.GetSecretUpdaterRole(),
			Members: saMembers,
			Condition: &expr.Expr{
				Expression:  BuildSecretUpdaterRoleConditionExpression(collection),
				Title:       GetSecretsUpdaterConditionTitle(collection),
				Description: GetSecretsUpdaterConditionDescription(collection),
			},
		})
	}

	// Per claimed group: viewer/updater bindings for the group principal, chunked so each
	// binding condition stays within GCP's IAM limits.
	for _, name := range groupNames {
		groupCfg := groupConfig.Groups[name]
		if len(groupCfg.SecretCollections) == 0 {
			continue
		}
		email := fmt.Sprintf("%s@redhat.com", name)
		groupMembers := []string{fmt.Sprintf("group:%s", email)}

		collections := make([]string, len(groupCfg.SecretCollections))
		copy(collections, groupCfg.SecretCollections)
		sort.Strings(collections)

		for chunkIdx, chunk := range chunkCollections(collections, MaxCollectionsPerGroupBinding) {
			desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
				Role:    config.GetSecretAccessorRole(),
				Members: groupMembers,
				Condition: &expr.Expr{
					Expression:  BuildSecretAccessorRoleConditionExpressionForCollections(chunk),
					Title:       GetSecretsViewerGroupConditionTitle(name, chunkIdx),
					Description: GetSecretsViewerGroupConditionDescription(name, chunkIdx),
				},
			})
			desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
				Role:    config.GetSecretUpdaterRole(),
				Members: groupMembers,
				Condition: &expr.Expr{
					Expression:  BuildSecretUpdaterRoleConditionExpressionForCollections(chunk),
					Title:       GetSecretsUpdaterGroupConditionTitle(name, chunkIdx),
					Description: GetSecretsUpdaterGroupConditionDescription(name, chunkIdx),
				},
			})
		}
	}

	return desiredSAs, desiredSecrets, desiredIAMBindings, desiredCollections, nil
}
