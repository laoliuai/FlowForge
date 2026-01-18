package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Tenant struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	Name        string    `gorm:"uniqueIndex;not null"`
	Description string
	QuotaID     *uuid.UUID   `gorm:"type:uuid"`
	Quota       *Quota       `gorm:"foreignKey:QuotaID"`
	Groups      []Group      `gorm:"foreignKey:TenantID"`
	Departments []Department `gorm:"foreignKey:TenantID"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`
}

type Department struct {
	ID        uuid.UUID    `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	TenantID  uuid.UUID    `gorm:"type:uuid;not null"`
	Tenant    *Tenant      `gorm:"foreignKey:TenantID"`
	Name      string       `gorm:"not null"`
	ParentID  *uuid.UUID   `gorm:"type:uuid"`
	Parent    *Department  `gorm:"foreignKey:ParentID"`
	Children  []Department `gorm:"foreignKey:ParentID"`
	QuotaID   *uuid.UUID   `gorm:"type:uuid"`
	Quota     *Quota       `gorm:"foreignKey:QuotaID"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Group struct {
	ID           uuid.UUID   `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	TenantID     uuid.UUID   `gorm:"type:uuid;not null"`
	Tenant       *Tenant     `gorm:"foreignKey:TenantID"`
	DepartmentID *uuid.UUID  `gorm:"type:uuid"`
	Department   *Department `gorm:"foreignKey:DepartmentID"`
	Name         string      `gorm:"not null"`
	Description  string
	QuotaID      *uuid.UUID `gorm:"type:uuid"`
	Quota        *Quota     `gorm:"foreignKey:QuotaID"`
	Projects     []Project  `gorm:"foreignKey:GroupID"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    gorm.DeletedAt `gorm:"index"`
}

type Project struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	GroupID     uuid.UUID `gorm:"type:uuid;not null"`
	Group       *Group    `gorm:"foreignKey:GroupID"`
	Name        string    `gorm:"not null"`
	Description string
	Namespace   string     `gorm:"not null"`
	QuotaID     *uuid.UUID `gorm:"type:uuid"`
	Quota       *Quota     `gorm:"foreignKey:QuotaID"`
	Workflows   []Workflow `gorm:"foreignKey:ProjectID"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`
}
