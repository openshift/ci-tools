package util

import (
	"fmt"
)

const (
	// ServiceDomain is the domain under which services are
	// routed for the current service cluster.
	ServiceDomain = "svc.ci.openshift.org"
)

func URLForService(service string) string {
	return fmt.Sprintf("https://%s", DomainForService(service))
}

func DomainForService(service string) string {
	return fmt.Sprintf("%s.%s", service, ServiceDomain)
}
