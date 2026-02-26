package rbac

import "slices"

var supportedRoles = []string{"admin", "operator", "viewer"}

func SupportedRoles() []string {
	out := make([]string, len(supportedRoles))
	copy(out, supportedRoles)
	return out
}

func IsSupportedRole(role string) bool {
	return slices.Contains(supportedRoles, role)
}
