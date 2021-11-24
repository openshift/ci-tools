package api

import (
	"fmt"
	"regexp"
)

const (
	// ServiceDomain is the domain under which services are
	// routed for the current service cluster.
	ServiceDomainCI    = "ci.openshift.org"
	ServiceDomainAPPCI = "apps.ci.l2s4.p1.openshiftapps.com"

	ServiceDomainAPPCIRegistry   = "registry.ci.openshift.org"
	ServiceDomainVSphereRegistry = "registry.apps.build01-us-west-2.vmc.ci.openshift.org"
)

type Service string

const (
	ServiceBoskos   Service = "boskos-ci"
	ServiceRegistry Service = "registry"
	ServiceRPMs     Service = "artifacts-rpms-openshift-origin-ci-rpms"
	ServiceProw     Service = "prow"
	ServiceConfig   Service = "config"
	ServiceGCSWeb   Service = "gcsweb-ci"
)

// URLForService returns the URL for the service including scheme
func URLForService(service Service) string {
	return fmt.Sprintf("https://%s", DomainForService(service))
}

// DomainForService returns the DNS domain name for the service
func DomainForService(service Service) string {
	var serviceDomain string
	switch service {
	case ServiceBoskos, ServiceGCSWeb:
		serviceDomain = ServiceDomainAPPCI
	case ServiceRPMs:
		serviceDomain = ServiceDomainAPPCI
	default:
		serviceDomain = ServiceDomainCI
	}
	return fmt.Sprintf("%s.%s", service, serviceDomain)
}

var (
	buildClusterRegEx = regexp.MustCompile(`build\d\d+`)
)

func RegistryDomainForClusterName(clusterName string) (string, error) {
	if clusterName == string(ClusterAPPCI) {
		return ServiceDomainAPPCIRegistry, nil
	}
	if clusterName == string(ClusterVSphere) {
		return ServiceDomainVSphereRegistry, nil
	}
	if buildClusterRegEx.MatchString(clusterName) {
		return fmt.Sprintf("registry.%s.ci.openshift.org", clusterName), nil
	}
	return "", fmt.Errorf("failed to get the domain for cluster %s", clusterName)
}
