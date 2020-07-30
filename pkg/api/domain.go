package api

import (
	"fmt"
)

const (
	// ServiceDomain is the domain under which services are
	// routed for the current service cluster.
	ServiceDomainCI    = "ci.openshift.org"
	ServiceDomainAPICI = "svc.ci.openshift.org"
	ServiceDomainAPPCI = "apps.ci.l2s4.p1.openshiftapps.com"
)

type Service string

const (
	ServiceBoskos   Service = "boskos-ci"
	ServiceRegistry Service = "registry"
	ServiceRPMs     Service = "rpms"
	ServiceProw     Service = "prow"
	ServiceConfig   Service = "config"
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
	case ServiceRPMs, ServiceRegistry:
		serviceDomain = ServiceDomainAPICI
	default:
		serviceDomain = ServiceDomainCI
	}
	return fmt.Sprintf("%s.%s", service, serviceDomain)
}
