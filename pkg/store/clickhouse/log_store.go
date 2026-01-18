package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/store"
)

type ClickHouseLogStore struct {
	conn   driver.Conn
	logger *zap.Logger
}

func NewClickHouseLogStore(addr string, database string, username string, password string, logger *zap.Logger) (*ClickHouseLogStore, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{
			Database: database,
			Username: username,
			Password: password,
		},
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
		// Settings: clickhouse.Settings{
		// 	"max_execution_time": 60,
		// },
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to clickhouse: %w", err)
	}

	if err := conn.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping clickhouse: %w", err)
	}

	return &ClickHouseLogStore{
		conn:   conn,
		logger: logger,
	}, nil
}

func (s *ClickHouseLogStore) CreateBatch(ctx context.Context, logs []*model.LogEntry) error {
	if len(logs) == 0 {
		return nil
	}

	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO task_logs")
	if err != nil {
		return err
	}

	for _, log := range logs {
		err := batch.Append(
			log.TaskID,
			log.WorkflowID,
			log.Timestamp,
			log.Level,
			log.Message,
			log.LineNum,
			time.Now(), // created_at
		)
		if err != nil {
			return err
		}
	}

	return batch.Send()
}

func (s *ClickHouseLogStore) List(ctx context.Context, taskID string, sinceTime *time.Time, limit int) ([]model.LogEntry, error) {
	taskUUID, err := uuid.Parse(taskID)
	if err != nil {
		return nil, fmt.Errorf("invalid task id: %w", err)
	}

	query := "SELECT task_id, workflow_id, timestamp, level, message, line_num FROM task_logs WHERE task_id = ?"
	args := []interface{}{taskUUID}

	if sinceTime != nil {
		query += " AND created_at > ?"
		args = append(args, *sinceTime)
	}

	query += " ORDER BY timestamp ASC, line_num ASC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []model.LogEntry
	for rows.Next() {
		var log model.LogEntry
		if err := rows.Scan(
			&log.TaskID,
			&log.WorkflowID,
			&log.Timestamp,
			&log.Level,
			&log.Message,
			&log.LineNum,
		); err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}

	return logs, nil
}

func (s *ClickHouseLogStore) Query(ctx context.Context, query store.LogQuery) ([]model.LogEntry, error) {
	if query.WorkflowID == "" {
		return nil, fmt.Errorf("workflow id is required")
	}

	workflowUUID, err := uuid.Parse(query.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("invalid workflow id: %w", err)
	}

	queryText := "SELECT task_id, workflow_id, timestamp, level, message, line_num FROM task_logs WHERE workflow_id = ?"
	args := []interface{}{workflowUUID}

	if query.TaskID != "" {
		taskUUID, err := uuid.Parse(query.TaskID)
		if err != nil {
			return nil, fmt.Errorf("invalid task id: %w", err)
		}
		queryText += " AND task_id = ?"
		args = append(args, taskUUID)
	}

	if query.StartTime != nil {
		queryText += " AND timestamp >= ?"
		args = append(args, *query.StartTime)
	}

	if query.EndTime != nil {
		queryText += " AND timestamp <= ?"
		args = append(args, *query.EndTime)
	}

	if query.Level != "" {
		queryText += " AND level = ?"
		args = append(args, query.Level)
	}

	if query.Search != "" {
		queryText += " AND message LIKE ?"
		args = append(args, "%"+query.Search+"%")
	}

	queryText += " ORDER BY timestamp ASC, line_num ASC"

	if query.Limit > 0 {
		queryText += fmt.Sprintf(" LIMIT %d", query.Limit)
	}

	rows, err := s.conn.Query(ctx, queryText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []model.LogEntry
	for rows.Next() {
		var log model.LogEntry
		if err := rows.Scan(
			&log.TaskID,
			&log.WorkflowID,
			&log.Timestamp,
			&log.Level,
			&log.Message,
			&log.LineNum,
		); err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}

	return logs, nil
}

func (s *ClickHouseLogStore) DeleteOldLogs(ctx context.Context, retentionDays int) error {
	// ClickHouse handles retention via TTL natively, so this can be a no-op
	// Or we can force optimization: "OPTIMIZE TABLE task_logs FINAL"
	return nil
}

func (s *ClickHouseLogStore) Close() error {
	return s.conn.Close()
}

// EnsureSchema creates the table if not exists
func (s *ClickHouseLogStore) EnsureSchema(ctx context.Context) error {
	query := `
	CREATE TABLE IF NOT EXISTS task_logs (
		task_id UUID,
		workflow_id UUID,
		timestamp Int64 Codec(Delta, ZSTD),
		level LowCardinality(String),
		message String Codec(ZSTD),
		line_num Int32,
		created_at DateTime DEFAULT now()
	)
	ENGINE = MergeTree()
	ORDER BY (task_id, timestamp)
	PARTITION BY toYYYYMMDD(created_at)
	TTL created_at + INTERVAL 7 DAY
	`
	return s.conn.Exec(ctx, query)
}
