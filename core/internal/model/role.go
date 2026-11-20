package model

const (
	RoleUnknown   = "unknown"
	RoleReadWrite = "read-write"
	RoleStandby   = "standby"
)

// RoleFromRecovery reports only what PostgreSQL exposes reliably. A server
// outside recovery is writable, but is not necessarily part of a primary/
// standby topology.
func RoleFromRecovery(inRecovery bool) string {
	if inRecovery {
		return RoleStandby
	}
	return RoleReadWrite
}
