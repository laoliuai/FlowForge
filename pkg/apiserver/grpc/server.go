package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/flowforge/flowforge/pkg/api/proto"
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
}

func NewServer(db *postgres.Store, redis *redisclient.Client, logRepo store.LogStore, logger *zap.Logger) *Server {
	return &Server{
		db:      db,
		redis:   redis,
		logger:  logger,
		logRepo: logRepo,
	}
}

func (s *Server) UpdateStatus(ctx context.Context, req *pb.UpdateStatusRequest) (*pb.UpdateStatusResponse, error) {
	// Implementation for status update (to be migrated from HTTP or kept as alternative)
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
