package multiarchbuildconfig

import (
	v1 "github.com/openshift/ci-tools/pkg/api/multiarchbuildconfig/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func getCondition(mabc *v1.MultiArchBuildConfig, condType string) *metav1.Condition {
	for i := range mabc.Status.Conditions {
		c := &mabc.Status.Conditions[i]
		if c.Type == condType {
			return c
		}
	}
	return nil
}

func setCondition(mabc *v1.MultiArchBuildConfig, cond *metav1.Condition) {
	for i := range mabc.Status.Conditions {
		current := &mabc.Status.Conditions[i]
		if current.Type == cond.Type {
			mabc.Status.Conditions[i] = *current
			return
		}
	}
	mabc.Status.Conditions = append(mabc.Status.Conditions, *cond)
}
