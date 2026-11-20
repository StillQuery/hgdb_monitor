package model

import (
	"time"

	"hgdb_monitor/core/internal/datasource"
)

// Datasource contains configuration needed by the local core process. It is
// page-facing data and must never be returned by an AI tool without redaction.
// Passwords are deliberately not part of this model or the SQLite schema.
type Datasource struct {
	ID                string                  `json:"id"`
	InstanceRef       string                  `json:"instanceRef"`
	DisplayName       string                  `json:"displayName"`
	Host              string                  `json:"host"`
	Port              uint16                  `json:"port"`
	Database          string                  `json:"database"`
	User              string                  `json:"user"`
	SSLMode           string                  `json:"sslMode"`
	ConnectTimeout    int                     `json:"connectTimeoutSeconds"`
	QueryTimeout      int                     `json:"queryTimeoutSeconds"`
	Capabilities      datasource.Capabilities `json:"capabilities"`
	PersistentEnabled bool                    `json:"persistentEnabled"`
	VerifiedAt        *time.Time              `json:"verifiedAt,omitempty"`
	CreatedAt         time.Time               `json:"createdAt"`
	UpdatedAt         time.Time               `json:"updatedAt"`
}
