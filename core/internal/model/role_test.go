package model

import "testing"

func TestRoleFromRecovery(t *testing.T) {
	tests := []struct {
		name       string
		inRecovery bool
		want       string
	}{
		{name: "standby while recovery is active", inRecovery: true, want: RoleStandby},
		{name: "read-write when recovery is inactive", inRecovery: false, want: RoleReadWrite},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RoleFromRecovery(test.inRecovery); got != test.want {
				t.Fatalf("RoleFromRecovery(%v) = %q, want %q", test.inRecovery, got, test.want)
			}
		})
	}
}
