package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/sirupsen/logrus"
)

func ComputeDiff(
	desiredSAs []ServiceAccountInfo,
	actualSAs []ServiceAccountInfo,
	desiredSecrets map[string]GCPSecret,
	actualSecrets map[string]GCPSecret,
	desiredIAMBindings []*iampb.Binding,
	actualIAMPolicy *iampb.Policy,
) Actions {
	var actions Actions

	actions.SAsToCreate, actions.SAsToDelete = DiffServiceAccounts(desiredSAs, actualSAs)
	actions.SecretsToCreate, actions.SecretsToDelete = DiffSecrets(desiredSecrets, actualSecrets)
	actions.ConsolidatedIAMPolicy = DiffIAMBindings(desiredIAMBindings, actualIAMPolicy)

	return actions
}

func DiffServiceAccounts(desiredSAs []ServiceAccountInfo, actualSAs []ServiceAccountInfo) (toCreate SAMap, toDelete SAMap) {
	desiredSAsMap := make(SAMap)
	for _, sa := range desiredSAs {
		desiredSAsMap[sa.Email] = sa
	}
	actualSAsMap := make(SAMap)
	for _, sa := range actualSAs {
		actualSAsMap[sa.Email] = sa
	}

	toCreate = make(SAMap)
	for _, desiredSA := range desiredSAs {
		if _, found := actualSAsMap[desiredSA.Email]; !found {
			toCreate[desiredSA.Email] = desiredSA
		}
	}

	toDelete = make(SAMap)
	for _, actualSA := range actualSAs {
		if _, found := desiredSAsMap[actualSA.Email]; !found {
			toDelete[actualSA.Email] = actualSA
		}
	}

	return toCreate, toDelete
}

func DiffSecrets(desiredSecrets, actualSecrets map[string]GCPSecret) (map[string]GCPSecret, []GCPSecret) {
	toCreate := make(map[string]GCPSecret)
	var toDelete []GCPSecret

	for _, secret := range desiredSecrets {
		if _, found := actualSecrets[secret.Name]; !found {
			toCreate[secret.Name] = secret
		}
	}

	for _, secret := range actualSecrets {
		if !isManagedBySecretSync(secret) {
			continue
		}
		if _, found := desiredSecrets[secret.Name]; !found {
			switch secret.Type {
			case SecretTypeSA:
				toDelete = append(toDelete, secret)
				logrus.Debugf("Scheduling SA secret '%s' for deletion (collection completely removed from config)", secret.Name)
			case SecretTypeIndex:
				// Preserve index secrets for now (partial cleanup)
				// TODO: Add scheduling logic for eventual cleanup?
			case SecretTypeUnknown:
				logrus.Warnf("Found managed secret with unrecognized naming pattern: %s", secret.Name)
			}
		}
	}

	return toCreate, toDelete
}

func classifySecret(secretName string) SecretType {
	if strings.HasSuffix(secretName, updaterSASecretSuffix) {
		return SecretTypeSA
	}
	if strings.HasSuffix(secretName, indexSecretSuffix) {
		return SecretTypeIndex
	}
	return SecretTypeUnknown
}

func DiffIAMBindings(desiredBindings []*iampb.Binding, actualPolicy *iampb.Policy) *iampb.Policy {
	desiredBindingsMap := make(map[string]*iampb.Binding)
	for _, binding := range desiredBindings {
		key := ToCanonicalIAMBinding(binding).makeCanonicalKey()
		desiredBindingsMap[key] = binding
	}

	actualBindingsMap := make(map[string]*iampb.Binding)
	var unmanagedBindings []*iampb.Binding
	for _, binding := range actualPolicy.Bindings {
		key := ToCanonicalIAMBinding(binding).makeCanonicalKey()
		actualBindingsMap[key] = binding

		if !isManagedBinding(binding) {
			unmanagedBindings = append(unmanagedBindings, binding)
		}
	}

	hasChanges := false
	var finalBindings []*iampb.Binding

	for _, desiredBinding := range desiredBindings {
		key := ToCanonicalIAMBinding(desiredBinding).makeCanonicalKey()
		if _, exists := actualBindingsMap[key]; !exists {
			hasChanges = true
		}
		finalBindings = append(finalBindings, desiredBinding)
	}

	for _, actualBinding := range actualPolicy.Bindings {
		actualKey := ToCanonicalIAMBinding(actualBinding).makeCanonicalKey()
		if _, foundInDesired := desiredBindingsMap[actualKey]; !foundInDesired && isManagedBinding(actualBinding) {
			logrus.Debugf("Removing obsolete IAM binding: Role=%s, Members=%s, Condition=%s", actualBinding.Role, actualBinding.Members, actualBinding.Condition.GetTitle())
			hasChanges = true
		}
	}

	finalBindings = append(finalBindings, unmanagedBindings...)

	if !hasChanges {
		return nil
	}

	consolidatedPolicy := &iampb.Policy{
		Bindings:     finalBindings,
		Etag:         actualPolicy.Etag,
		Version:      3, // required for IAM conditions support
		AuditConfigs: actualPolicy.AuditConfigs,
	}
	return consolidatedPolicy
}

func isManagedBySecretSync(secret GCPSecret) bool {
	return secret.Labels["managed-by"] == SecretSyncLabel &&
		(strings.HasSuffix(secret.Name, indexSecretSuffix) || strings.HasSuffix(secret.Name, updaterSASecretSuffix))
}

func isManagedBinding(b *iampb.Binding) bool {
	if !(b.Role == SecretAccessorRole || b.Role == SecretUpdaterRole) {
		return false
	}
	if b.Condition == nil {
		return false
	}

	// TODO: make this better somehow?
	// Check if condition expression matches our patterns
	expr := b.Condition.Expression

	// Look for our characteristic patterns in the condition
	hasSecretManagerResource := strings.Contains(expr, "secretmanager.googleapis.com")
	hasSecretExtract := strings.Contains(expr, `resource.name.extract("secrets/{secret}/")`)
	hasStartsWithPattern := strings.Contains(expr, "startsWith(") || strings.Contains(expr, "==")

	return hasSecretManagerResource && hasSecretExtract && hasStartsWithPattern
}

// ToCanonicalIAMBinding converts an iampb.Binding into our canonical form.
// This is necessary for consistent key generation and comparison.
func ToCanonicalIAMBinding(b *iampb.Binding) CanonicalIAMBinding {
	members := make([]string, len(b.Members))
	copy(members, b.Members)
	sort.Strings(members)

	conditionExpr := ""
	if b.Condition != nil {
		conditionExpr = b.Condition.Expression
	}

	return CanonicalIAMBinding{
		Role:      b.Role,
		Members:   strings.Join(members, ","),
		Condition: conditionExpr,
	}
}

func (c CanonicalIAMBinding) makeCanonicalKey() string {
	jsonData, err := json.Marshal(c)
	if err != nil {
		logrus.Fatal(err)
	}
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:])
}
