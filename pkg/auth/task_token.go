package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/flowforge/flowforge/pkg/model"
)

var ErrInvalidToken = errors.New("invalid token")

type TaskTokenClaims struct {
	jwt.RegisteredClaims
	TaskID     string `json:"task_id"`
	WorkflowID string `json:"workflow_id"`
	TenantID   string `json:"tenant_id"`
	ProjectID  string `json:"project_id"`
	Scope      string `json:"scope"`
}

type TaskTokenManager struct {
	signingKey []byte
	ttl        time.Duration
}

func NewTaskTokenManager(signingKey []byte, ttl time.Duration) *TaskTokenManager {
	return &TaskTokenManager{signingKey: signingKey, ttl: ttl}
}

func (m *TaskTokenManager) GenerateTaskToken(task *model.Task, tenantID, projectID string) (string, error) {
	claims := TaskTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(m.ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   task.ID.String(),
			Issuer:    "flowforge",
		},
		TaskID:     task.ID.String(),
		WorkflowID: task.WorkflowID.String(),
		TenantID:   tenantID,
		ProjectID:  projectID,
		Scope:      "status,logs,metrics,outputs,checkpoint",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.signingKey)
}

func (m *TaskTokenManager) ValidateTaskToken(tokenString string) (*TaskTokenClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &TaskTokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		return m.signingKey, nil
	})

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*TaskTokenClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

func (c *TaskTokenClaims) HasScope(required string) bool {
	scopes := strings.Split(c.Scope, ",")
	for _, scope := range scopes {
		if scope == required {
			return true
		}
	}
	return false
}
