package runtimecontract

const (
	DefaultControlNamespace = "holon-system"
	APIServiceName          = "holon-api"
	AuthzServiceName        = "holon-authz"
)

// Principal is the mesh identity of a service account.
func Principal(namespace, serviceAccount string) string {
	return "cluster.local/ns/" + namespace + "/sa/" + serviceAccount
}
