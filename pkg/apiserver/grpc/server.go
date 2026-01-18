package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/flowforge/flowforge/pkg/api/proto"
	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/store"
	"github.com/flowforge/flowforge/pkg/store/postgres"
	redisclient "github.com/flowforge/flowforge/pkg/store/redis"
)

type Server struct {
	pb.UnimplementedTaskServiceServer
	db      *postgres.Store
	redis   *redisclient.Client
	logger  *zap.Logger
	logRepo store.LogStore
	bus     *eventbus.Bus
}

func NewServer(db *postgres.Store, redis *redisclient.Client, logRepo store.LogStore, logger *zap.Logger) *Server {
	return &Server{
		db:      db,
		redis:   redis,
		logger:  logger,
		logRepo: logRepo,
		bus:     eventbus.NewBus(redis.Client()),
	}
}

func (s *Server) UpdateStatus(ctx context.Context, req *pb.UpdateStatusRequest) (*pb.UpdateStatusResponse, error) {
	taskIDRaw := strings.TrimSpace(req.GetTaskId())
	workflowIDRaw := strings.TrimSpace(req.GetWorkflowId())
	statusRaw := strings.TrimSpace(req.GetStatus())

	if taskIDRaw == "" || workflowIDRaw == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id and workflow_id are required")
	}

	taskID, err := uuid.Parse(taskIDRaw)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid task_id: %v", err)
	}

	workflowID, err := uuid.Parse(workflowIDRaw)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid workflow_id: %v", err)
	}

	if statusRaw == "" {
		return nil, status.Error(codes.InvalidArgument, "status is required")
	}

	normalizedStatus := model.TaskStatus(strings.ToUpper(statusRaw))
	if !isValidTaskStatus(normalizedStatus) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid status: %s", statusRaw)
	}

	taskEvent := eventbus.TaskEvent{
		TaskID:     taskID.String(),
		WorkflowID: workflowID.String(),
		Status:     string(normalizedStatus),
		Message:    strings.TrimSpace(req.GetMessage()),
	}

	event, err := eventbus.NewEvent("task_status", taskEvent)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to build task event: %v", err)
	}

	if err := s.bus.Publish(ctx, eventbus.ChannelTask, event); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to publish task event: %v", err)
	}

	return &pb.UpdateStatusResponse{Success: true}, nil
}

func (s *Server) StreamLogs(stream pb.TaskService_StreamLogsServer) error {
	const batchSize = 100
	const flushInterval = 1 * time.Second

	batch := make([]*model.LogEntry, 0, batchSize)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}

		// Push to DB (Cold path)
		if err := s.logRepo.CreateBatch(context.Background(), batch); err != nil {
			s.logger.Error("failed to persist logs", zap.Error(err))
		}

		// Push to Redis (Hot path)
		pipe := s.redis.Client().Pipeline()
		for _, entry := range batch {
			// Pub/Sub for real-time
			channel := fmt.Sprintf("logs:task:%s", entry.TaskID.String())
			payload, _ := json.Marshal(entry)
			pipe.Publish(context.Background(), channel, payload)

			// List for buffer
			listKey := fmt.Sprintf("logs:buffer:%s", entry.TaskID.String())
			pipe.RPush(context.Background(), listKey, payload)
			pipe.Expire(context.Background(), listKey, 1*time.Hour)
		}
		if _, err := pipe.Exec(context.Background()); err != nil {
			s.logger.Error("failed to push logs to redis", zap.Error(err))
		}

		batch = batch[:0]
		return nil
	}

	for {
		select {
		case <-ticker.C:
			if err := flush(); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return flush()
		default:
			entry, err := stream.Recv()
			if err == io.EOF {
				return flush()
			}
			if err != nil {
				return err
			}

			taskID, _ := uuid.Parse(entry.TaskId)
			workflowID, _ := uuid.Parse(entry.WorkflowId)

			batch = append(batch, &model.LogEntry{
				TaskID:     taskID,
				WorkflowID: workflowID,
				Timestamp:  entry.Timestamp,
				Level:      entry.Level,
				Message:    entry.Message,
				LineNum:    entry.LineNum,
			})

			if len(batch) >= batchSize {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
}

func (s *Server) Register(server *grpc.Server) {
	pb.RegisterTaskServiceServer(server, s)
}

func isValidTaskStatus(status model.TaskStatus) bool {
	switch status {
	case model.TaskPending,
		model.TaskQueued,
		model.TaskRunning,
		model.TaskSucceeded,
		model.TaskFailed,
		model.TaskSkipped,
		model.TaskRetrying:
		return true
	default:
		return false
	}
}
