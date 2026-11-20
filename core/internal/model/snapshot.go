package model

import "time"

// Snapshot is the V1 set of 24 persisted metrics plus five latest-state facts.
// It contains no endpoint, account, client address or SQL text.
type Snapshot struct {
	CollectedAt time.Time `json:"collectedAt"`

	SessionsTotal           int64 `json:"sessionsTotal"`
	SessionsActive          int64 `json:"sessionsActive"`
	SessionsIdleTransaction int64 `json:"sessionsIdleTransaction"`
	SessionsWaiting         int64 `json:"sessionsWaiting"`
	LongQueries             int64 `json:"longQueries"`
	LongTransactions        int64 `json:"longTransactions"`

	Commits      int64 `json:"commits"`
	Rollbacks    int64 `json:"rollbacks"`
	BlocksHit    int64 `json:"blocksHit"`
	BlocksRead   int64 `json:"blocksRead"`
	RowsInserted int64 `json:"rowsInserted"`
	RowsUpdated  int64 `json:"rowsUpdated"`
	RowsDeleted  int64 `json:"rowsDeleted"`
	TempFiles    int64 `json:"tempFiles"`
	TempBytes    int64 `json:"tempBytes"`
	Deadlocks    int64 `json:"deadlocks"`

	LocksNotGranted int64 `json:"locksNotGranted"`
	BlockedSessions int64 `json:"blockedSessions"`

	WALBytes             int64 `json:"walBytes"`
	CheckpointsTimed     int64 `json:"checkpointsTimed"`
	CheckpointsRequested int64 `json:"checkpointsRequested"`

	ReplicationConnections       int64   `json:"replicationConnections"`
	MaxReplayLagBytes            int64   `json:"maxReplayLagBytes"`
	MaxReplayLagBytesAvailable   bool    `json:"maxReplayLagBytesAvailable"`
	MaxReplayLagSeconds          float64 `json:"maxReplayLagSeconds"`
	MaxReplayLagSecondsAvailable bool    `json:"maxReplayLagSecondsAvailable"`

	Connected       bool      `json:"connected"`
	PostmasterStart time.Time `json:"postmasterStart"`
	InRecovery      bool      `json:"inRecovery"`
	MaxConnections  int64     `json:"maxConnections"`
	LastCollectedAt time.Time `json:"lastCollectedAt"`
}
