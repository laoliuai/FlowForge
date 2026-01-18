package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/flowforge/flowforge/pkg/config"
	"github.com/flowforge/flowforge/pkg/model"
)

type Store struct {
	db *gorm.DB
}

func NewStore(cfg *config.DatabaseConfig) (*Store, error) {
	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	}

	db, err := gorm.Open(postgres.Open(cfg.DSN()), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)

	return &Store{db: db}, nil
}

func (s *Store) DB() *gorm.DB {
	return s.db
}

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (s *Store) AutoMigrate() error {
	return s.db.AutoMigrate(
		&model.Tenant{},
		&model.Department{},
		&model.Group{},
		&model.Project{},
		&model.Quota{},
		&model.QuotaUsage{},
		&model.Workflow{},
		&model.Task{},
		&model.TaskDependency{},
		&model.TaskEvent{},
	)
}

type WorkflowRepository struct {
	db *gorm.DB
}

func NewWorkflowRepository(db *gorm.DB) *WorkflowRepository {
	return &WorkflowRepository{db: db}
}

func (r *WorkflowRepository) Create(ctx context.Context, workflow *model.Workflow) error {
	return r.db.WithContext(ctx).Create(workflow).Error
}

func (r *WorkflowRepository) GetByID(ctx context.Context, id string) (*model.Workflow, error) {
	var workflow model.Workflow
	err := r.db.WithContext(ctx).
		Preload("Tasks").
		Preload("Tasks.Dependencies").
		First(&workflow, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &workflow, nil
}

func (r *WorkflowRepository) UpdateStatus(ctx context.Context, id string, status model.WorkflowStatus, errorMsg string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}

	if errorMsg != "" {
		updates["error_message"] = errorMsg
	}

	if status == model.WorkflowRunning {
		now := time.Now()
		updates["started_at"] = &now
	}

	if status == model.WorkflowSucceeded || status == model.WorkflowFailed || status == model.WorkflowCancelled {
		now := time.Now()
		updates["finished_at"] = &now
	}

	return r.db.WithContext(ctx).Model(&model.Workflow{}).Where("id = ?", id).Updates(updates).Error
}

func (r *WorkflowRepository) List(ctx context.Context, projectID string, status *model.WorkflowStatus, limit, offset int) ([]model.Workflow, int64, error) {
	var workflows []model.Workflow
	var total int64

	query := r.db.WithContext(ctx).Model(&model.Workflow{}).Where("project_id = ?", projectID)

	if status != nil {
		query = query.Where("status = ?", *status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&workflows).Error

	return workflows, total, err
}

type TaskRepository struct {
	db *gorm.DB
}

func NewTaskRepository(db *gorm.DB) *TaskRepository {
	return &TaskRepository{db: db}
}

func (r *TaskRepository) Create(ctx context.Context, task *model.Task) error {
	return r.db.WithContext(ctx).Create(task).Error
}

func (r *TaskRepository) CreateBatch(ctx context.Context, tasks []*model.Task) error {
	return r.db.WithContext(ctx).CreateInBatches(tasks, 100).Error
}

func (r *TaskRepository) GetByID(ctx context.Context, id string) (*model.Task, error) {
	var task model.Task
	err := r.db.WithContext(ctx).
		Preload("Dependencies").
		First(&task, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (r *TaskRepository) UpdateStatus(ctx context.Context, id string, status model.TaskStatus, updates map[string]interface{}) error {
	if updates == nil {
		updates = make(map[string]interface{})
	}
	updates["status"] = status
	updates["updated_at"] = time.Now()

	return r.db.WithContext(ctx).Model(&model.Task{}).Where("id = ?", id).Updates(updates).Error
}

func (r *TaskRepository) GetPendingTasks(ctx context.Context, workflowID string) ([]model.Task, error) {
	var tasks []model.Task
	err := r.db.WithContext(ctx).
		Preload("Dependencies").
		Where("workflow_id = ? AND status = ?", workflowID, model.TaskPending).
		Find(&tasks).Error
	return tasks, err
}

func (r *TaskRepository) GetRetryableTasks(ctx context.Context) ([]model.Task, error) {
	var tasks []model.Task
	err := r.db.WithContext(ctx).
		Where("status = ? AND next_retry_at <= ?", model.TaskRetrying, time.Now()).
		Find(&tasks).Error
	return tasks, err
}

func (r *TaskRepository) SetOutput(ctx context.Context, taskID, key string, value interface{}) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE tasks
		SET outputs = outputs || ?::jsonb, updated_at = NOW()
		WHERE id = ?
	`, fmt.Sprintf(`{"%s": %s}`, key, mustMarshal(value)), taskID).Error
}

func mustMarshal(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
