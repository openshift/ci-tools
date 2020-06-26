package api

import (
	"fmt"
)

const (
	// ServiceDomain is the domain under which services are
	// routed for the current service cluster.
	ServiceDomainAPICI = "svc.ci.openshift.org"
	ServiceDomainAPPCI = "apps.ci.l2s4.p1.openshiftapps.com"
)

type Service string

const (
	ServiceBoskos   Service = "boskos-ci"
	ServiceRegistry Service = "registry"
	ServiceRPMs     Service = "rpms"
	ServiceProw     Service = "prow"
)

// URLForService returns the URL for the service including scheme
func URLForService(service Service) string {
	return fmt.Sprintf("https://%s", DomainForService(service))
}

// DomainForService returns the DNS domain name for the service
func DomainForService(service Service) string {
	var serviceDomain string
	switch service {
	case ServiceBoskos:
		serviceDomain = ServiceDomainAPPCI
	default:
		serviceDomain = ServiceDomainAPICI
	}
	return fmt.Sprintf("%s.%s", service, serviceDomain)
}
