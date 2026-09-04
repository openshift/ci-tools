package gsmsecrets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/sirupsen/logrus"
)

// BuildSecretAccessorRoleConditionExpression builds the IAM condition expression for secret accessor role
func BuildSecretAccessorRoleConditionExpression(collection string) string {
	// Define the two specific secrets this role can access
	updaterSecret := fmt.Sprintf("%s%s", collection, UpdaterSASecretSuffix)
	indexSecret := fmt.Sprintf("%s%s", collection, IndexSecretSuffix)

	return fmt.Sprintf(`(
  resource.type == "secretmanager.googleapis.com/SecretVersion" ||
  resource.type == "secretmanager.googleapis.com/Secret"
) && (
  resource.name.extract("secrets/{secret}") == "%s" ||
  resource.name.extract("secrets/{secret}") == "%s"
)`, updaterSecret, indexSecret)
}

// BuildSecretUpdaterRoleConditionExpression builds the IAM condition expression for secret updater role
func BuildSecretUpdaterRoleConditionExpression(collection string) string {
	return fmt.Sprintf(`(
  resource.type == "secretmanager.googleapis.com/SecretVersion" ||
  resource.type == "secretmanager.googleapis.com/Secret"
) && 
  resource.name.extract("secrets/{secret}").startsWith("%s__")`, collection)
}

// BuildSecretAccessorRoleConditionExpressionForCollections builds the viewer IAM condition
// expression covering multiple collections. It is used for group bindings, where a group's
// collections are chunked to keep the number of logical operators within GCP's limits.
func BuildSecretAccessorRoleConditionExpressionForCollections(collections []string) string {
	var terms []string
	for _, collection := range collections {
		terms = append(terms,
			fmt.Sprintf(`  resource.name.extract("secrets/{secret}") == "%s%s"`, collection, UpdaterSASecretSuffix),
			fmt.Sprintf(`  resource.name.extract("secrets/{secret}") == "%s%s"`, collection, IndexSecretSuffix),
		)
	}
	return fmt.Sprintf(`(
  resource.type == "secretmanager.googleapis.com/SecretVersion" ||
  resource.type == "secretmanager.googleapis.com/Secret"
) && (
%s
)`, strings.Join(terms, " ||\n"))
}

// BuildSecretUpdaterRoleConditionExpressionForCollections builds the updater IAM condition
// expression covering multiple collections. It is used for group bindings, where a group's
// collections are chunked to keep the number of logical operators within GCP's limits.
func BuildSecretUpdaterRoleConditionExpressionForCollections(collections []string) string {
	var terms []string
	for _, collection := range collections {
		terms = append(terms, fmt.Sprintf(`  resource.name.extract("secrets/{secret}").startsWith("%s__")`, collection))
	}
	return fmt.Sprintf(`(
  resource.type == "secretmanager.googleapis.com/SecretVersion" ||
  resource.type == "secretmanager.googleapis.com/Secret"
) && (
%s
)`, strings.Join(terms, " ||\n"))
}

// chunkCollections splits a sorted slice of collections into consecutive chunks of at most
// size. Chunking is deterministic so binding conditions stay stable across reconciler runs.
func chunkCollections(collections []string, size int) [][]string {
	var chunks [][]string
	for i := 0; i < len(collections); i += size {
		end := min(i+size, len(collections))
		chunks = append(chunks, collections[i:end])
	}
	return chunks
}

// GetSecretsViewerConditionTitle returns the condition title for secrets viewer role
func GetSecretsViewerConditionTitle(collection string) string {
	return fmt.Sprintf("%s%s", SecretsViewerConditionTitlePrefix, collection)
}

// GetSecretsViewerGroupConditionTitle returns the viewer condition title for a group binding chunk.
func GetSecretsViewerGroupConditionTitle(group string, chunkIdx int) string {
	return fmt.Sprintf("%sgroup %s (set %d)", SecretsViewerConditionTitlePrefix, group, chunkIdx+1)
}

// GetSecretsUpdaterGroupConditionTitle returns the updater condition title for a group binding chunk.
func GetSecretsUpdaterGroupConditionTitle(group string, chunkIdx int) string {
	return fmt.Sprintf("%sgroup %s (set %d)", SecretsUpdaterConditionTitlePrefix, group, chunkIdx+1)
}

// GetSecretsViewerGroupConditionDescription returns the viewer condition description for a group binding chunk.
func GetSecretsViewerGroupConditionDescription(group string, chunkIdx int) string {
	return fmt.Sprintf("Managed by %s: Read access to secrets for group %s (set %d)", TestPlatform, group, chunkIdx+1)
}

// GetSecretsUpdaterGroupConditionDescription returns the updater condition description for a group binding chunk.
func GetSecretsUpdaterGroupConditionDescription(group string, chunkIdx int) string {
	return fmt.Sprintf("Managed by %s: Create, update, and delete access to secrets for group %s (set %d)", TestPlatform, group, chunkIdx+1)
}

// GetSecretsUpdaterConditionTitle returns the condition title for secrets updater role
func GetSecretsUpdaterConditionTitle(collection string) string {
	return fmt.Sprintf("%s%s", SecretsUpdaterConditionTitlePrefix, collection)
}

// GetSecretsViewerConditionDescription returns the condition description for secrets viewer role
func GetSecretsViewerConditionDescription(collection string) string {
	return fmt.Sprintf(SecretsViewerConditionDescriptionTemplate, TestPlatform, collection)
}

// GetSecretsUpdaterConditionDescription returns the condition description for secrets updater role
func GetSecretsUpdaterConditionDescription(collection string) string {
	return fmt.Sprintf(SecretsUpdaterConditionDescriptionTemplate, TestPlatform, collection)
}

// IsManagedBinding checks if an IAM binding is managed by this tool.
func IsManagedBinding(b *iampb.Binding) bool {
	isSecretAccessorRole := strings.Contains(b.Role, "/roles/openshift_ci_secrets_viewer")
	isSecretUpdaterRole := strings.Contains(b.Role, "/roles/openshift_ci_secrets_updater")
	if !(isSecretAccessorRole || isSecretUpdaterRole) {
		return false
	}
	if b.Condition == nil {
		return false
	}

	title := b.Condition.GetTitle()
	description := b.Condition.GetDescription()

	titleMatches := strings.HasPrefix(title, SecretsViewerConditionTitlePrefix) ||
		strings.HasPrefix(title, SecretsUpdaterConditionTitlePrefix)
	descriptionMatches := strings.HasPrefix(description, fmt.Sprintf("Managed by %s:", TestPlatform))

	if !titleMatches || !descriptionMatches {
		return false
	}

	expr := b.Condition.Expression
	hasSecretManagerResource := strings.Contains(expr, "secretmanager.googleapis.com")
	hasSecretExtract := strings.Contains(expr, `resource.name.extract("secrets/{secret}")`)
	hasExpectedPattern := strings.Contains(expr, "startsWith(") || strings.Contains(expr, "==")

	return hasSecretManagerResource && hasSecretExtract && hasExpectedPattern
}

// ToCanonicalIAMBinding converts an iampb.Binding into our canonical form.
// This is necessary for consistent key generation and comparison.
func ToCanonicalIAMBinding(b *iampb.Binding) CanonicalIAMBinding {
	members := make([]string, len(b.Members))
	copy(members, b.Members)
	sort.Strings(members)

	conditionExpr := ""
	conditionTitle := ""
	conditionDesc := ""
	if b.Condition != nil {
		conditionExpr = b.Condition.Expression
		conditionTitle = b.Condition.GetTitle()
		conditionDesc = b.Condition.GetDescription()
	}

	return CanonicalIAMBinding{
		Role:           b.Role,
		Members:        strings.Join(members, ","),
		ConditionExpr:  conditionExpr,
		ConditionTitle: conditionTitle,
		ConditionDesc:  conditionDesc,
	}
}

// makeCanonicalKey generates a canonical key for IAM binding comparison
func (c CanonicalIAMBinding) makeCanonicalKey() string {
	jsonData, err := json.Marshal(c)
	if err != nil {
		logrus.Fatal(err)
	}
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:])
}
