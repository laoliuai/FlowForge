package model

import (
	"time"

	"github.com/google/uuid"
)

type Quota struct {
	ID                uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	Name              string    `gorm:"not null"`
	CPULimit          int       `gorm:"default:4000"`
	CPURequest        int       `gorm:"default:2000"`
	MemoryLimit       int       `gorm:"default:8192"`
	MemoryRequest     int       `gorm:"default:4096"`
	GPULimit          int       `gorm:"default:0"`
	MaxConcurrentPods int       `gorm:"default:10"`
	MaxConcurrentWF   int       `gorm:"default:5"`
	StorageLimit      int       `gorm:"default:100"`
	PriorityWeight    int       `gorm:"default:100"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type QuotaUsage struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	QuotaID         uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_quota_entity"`
	Quota           *Quota    `gorm:"foreignKey:QuotaID"`
	EntityType      string    `gorm:"not null;uniqueIndex:idx_quota_entity"`
	EntityID        uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_quota_entity"`
	CPUUsed         int       `gorm:"default:0"`
	MemoryUsed      int       `gorm:"default:0"`
	GPUUsed         int       `gorm:"default:0"`
	PodsRunning     int       `gorm:"default:0"`
	WorkflowsActive int       `gorm:"default:0"`
	StorageUsed     int       `gorm:"default:0"`
	UpdatedAt       time.Time
}
