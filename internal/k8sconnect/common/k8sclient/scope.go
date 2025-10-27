package k8sclient

import "strings"

// clusterScopedResources is a comprehensive map of cluster-scoped Kubernetes resources.
// Keys are in the format "group.kind" (lowercase) or just "kind" for core resources.
// Based on kubectl api-resources --namespaced=false output and official K8s API reference.
var clusterScopedResources = map[string]bool{
	// Core (apiVersion: v1)
	"namespace":        true,
	"node":             true,
	"persistentvolume": true,

	// admissionregistration.k8s.io/v1
	"admissionregistration.k8s.io.mutatingwebhookconfiguration":     true,
	"admissionregistration.k8s.io.validatingwebhookconfiguration":   true,
	"admissionregistration.k8s.io.validatingadmissionpolicy":        true,
	"admissionregistration.k8s.io.validatingadmissionpolicybinding": true,

	// apiextensions.k8s.io/v1
	"apiextensions.k8s.io.customresourcedefinition": true,

	// apiregistration.k8s.io/v1
	"apiregistration.k8s.io.apiservice": true,

	// authentication.k8s.io/v1
	"authentication.k8s.io.tokenreview":       true,
	"authentication.k8s.io.selfsubjectreview": true,

	// authorization.k8s.io/v1
	"authorization.k8s.io.selfsubjectaccessreview": true,
	"authorization.k8s.io.selfsubjectrulesreview":  true,
	"authorization.k8s.io.subjectaccessreview":     true,

	// certificates.k8s.io/v1
	"certificates.k8s.io.certificatesigningrequest": true,

	// flowcontrol.apiserver.k8s.io/v1
	"flowcontrol.apiserver.k8s.io.flowschema":                 true,
	"flowcontrol.apiserver.k8s.io.prioritylevelconfiguration": true,

	// networking.k8s.io/v1
	"networking.k8s.io.ingressclass": true,

	// node.k8s.io/v1
	"node.k8s.io.runtimeclass": true,

	// rbac.authorization.k8s.io/v1
	"rbac.authorization.k8s.io.clusterrole":        true,
	"rbac.authorization.k8s.io.clusterrolebinding": true,

	// scheduling.k8s.io/v1
	"scheduling.k8s.io.priorityclass": true,

	// storage.k8s.io/v1
	"storage.k8s.io.csidriver":        true,
	"storage.k8s.io.csinode":          true,
	"storage.k8s.io.storageclass":     true,
	"storage.k8s.io.volumeattachment": true,
}

// IsClusterScopedResource returns true if the given apiVersion/kind combination
// represents a cluster-scoped resource. Returns false for unknown resources.
func IsClusterScopedResource(apiVersion, kind string) bool {
	// Extract group from apiVersion
	// apiVersion formats:
	// - "v1" -> no group (core)
	// - "apps/v1" -> group is "apps"
	// - "rbac.authorization.k8s.io/v1" -> group is "rbac.authorization.k8s.io"
	var group string
	if strings.Contains(apiVersion, "/") {
		parts := strings.Split(apiVersion, "/")
		group = strings.ToLower(parts[0])
	}

	// Normalize kind to lowercase
	kind = strings.ToLower(kind)

	// Build lookup key: "group.kind" or just "kind" for core resources
	var lookupKey string
	if group != "" {
		lookupKey = group + "." + kind
	} else {
		lookupKey = kind
	}

	return clusterScopedResources[lookupKey]
}

// IsClusterScopedKind returns true if the given kind is a common cluster-scoped resource.
// This is a simplified version that only checks core API resources (no group).
// For more accurate results with API groups, use IsClusterScopedResource().
func IsClusterScopedKind(kind string) bool {
	return clusterScopedResources[strings.ToLower(kind)]
}
