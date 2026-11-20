package model

import "time"

// InstanceStatus contains current facts refreshed independently from history.
type InstanceStatus struct {
	ObservedAt      time.Time `json:"observedAt"`
	Connected       bool      `json:"connected"`
	PostmasterStart time.Time `json:"postmasterStart"`
	InRecovery      bool      `json:"inRecovery"`
	MaxConnections  int64     `json:"maxConnections"`
}

func ApplyStatus(snapshot *Snapshot, status InstanceStatus) {
	snapshot.Connected = status.Connected
	snapshot.PostmasterStart = status.PostmasterStart
	snapshot.InRecovery = status.InRecovery
	snapshot.MaxConnections = status.MaxConnections
}
