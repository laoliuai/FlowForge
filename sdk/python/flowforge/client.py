import os
import time
import json
import atexit
import threading
from queue import Queue, Empty
from typing import Any, Dict, Optional, Union
from datetime import datetime
from dataclasses import dataclass

import grpc
import redis
import requests
from .proto import flowforge_pb2, flowforge_pb2_grpc
from .types import TaskStatus, LogLevel, MetricType


@dataclass
class Config:
    """SDK Configuration from environment variables."""

    task_id: str
    workflow_id: str
    tenant_id: str
    project_id: str
    api_endpoint: str
    redis_endpoint: str
    log_endpoint: str
    auth_token: str
    heartbeat_interval: int = 5
    log_batch_size: int = 100
    log_flush_interval: float = 1.0
    metric_flush_interval: float = 5.0


class FlowForgeClient:
    """
    FlowForge SDK Client for task status reporting, logging, and metrics.

    This client is designed to be used inside workflow task pods to:
    - Report task status and progress
    - Stream logs to centralized storage
    - Emit custom metrics
    - Store task outputs
    - Send heartbeats

    Example:
        from flowforge import client

        client.log("Starting processing")
        client.status("RUNNING", progress=10)

        for i, item in enumerate(items):
            process(item)
            client.metric("items_processed", i + 1)
            client.status("RUNNING", progress=int((i + 1) / len(items) * 100))

        client.output("result", {"processed": len(items)})
        client.status("SUCCEEDED")
    """

    def __init__(self, config: Config):
        self.config = config
        self._log_queue: Queue = Queue()
        self._metric_queue: Queue = Queue()
        self._shutdown = threading.Event()

        # Initialize connections
        self._init_grpc()
        self._init_redis()
        self._init_http_session()

        # Start background workers
        self._start_workers()

        # Register cleanup
        atexit.register(self.close)

    @classmethod
    def from_env(cls) -> "FlowForgeClient":
        """Create client from environment variables."""
        config = Config(
            task_id=os.environ["FLOWFORGE_TASK_ID"],
            workflow_id=os.environ["FLOWFORGE_WORKFLOW_ID"],
            tenant_id=os.environ["FLOWFORGE_TENANT_ID"],
            project_id=os.environ["FLOWFORGE_PROJECT_ID"],
            api_endpoint=os.environ["FLOWFORGE_API_ENDPOINT"],
            redis_endpoint=os.environ["FLOWFORGE_REDIS_ENDPOINT"],
            log_endpoint=os.environ["FLOWFORGE_LOG_ENDPOINT"],
            auth_token=os.environ["FLOWFORGE_AUTH_TOKEN"],
            heartbeat_interval=int(os.environ.get("FLOWFORGE_HEARTBEAT_INTERVAL", "5")),
        )
        return cls(config)

    def _init_grpc(self):
        """Initialize gRPC channel for status reporting."""
        self._grpc_channel = grpc.insecure_channel(self.config.api_endpoint)
        self._status_stub = flowforge_pb2_grpc.TaskServiceStub(self._grpc_channel)

    def _init_redis(self):
        """Initialize Redis connection for heartbeats."""
        host, port = self.config.redis_endpoint.split(":")
        self._redis = redis.Redis(host=host, port=int(port), decode_responses=True)

    def _init_http_session(self):
        """Initialize HTTP session for log streaming."""
        self._http_session = requests.Session()
        self._http_session.headers.update(
            {
                "Authorization": f"Bearer {self.config.auth_token}",
                "Content-Type": "application/json",
            }
        )

    def _start_workers(self):
        """Start background worker threads."""
        # Heartbeat worker
        self._heartbeat_thread = threading.Thread(
            target=self._heartbeat_worker,
            daemon=True,
            name="flowforge-heartbeat",
        )
        self._heartbeat_thread.start()

        # Log flusher worker
        self._log_thread = threading.Thread(
            target=self._log_worker,
            daemon=True,
            name="flowforge-log",
        )
        self._log_thread.start()

        # Metric flusher worker
        self._metric_thread = threading.Thread(
            target=self._metric_worker,
            daemon=True,
            name="flowforge-metric",
        )
        self._metric_thread.start()

    # ==================== Public API ====================

    def status(
        self,
        status: Union[str, TaskStatus],
        message: Optional[str] = None,
        progress: Optional[int] = None,
        details: Optional[Dict[str, Any]] = None,
    ) -> None:
        """
        Update task status.

        Args:
            status: Task status (RUNNING, SUCCEEDED, FAILED, etc.)
            message: Optional status message
            progress: Optional progress percentage (0-100)
            details: Optional additional details

        Example:
            client.status("RUNNING", progress=50, message="Processing batch 5/10")
        """
        if isinstance(status, TaskStatus):
            status = status.value

        request = flowforge_pb2.UpdateStatusRequest(
            task_id=self.config.task_id,
            workflow_id=self.config.workflow_id,
            status=status,
            message=message or "",
            progress=progress or 0,
            details=json.dumps(details or {}),
            timestamp=int(time.time() * 1000),
        )

        try:
            self._status_stub.UpdateStatus(request, timeout=5)
        except grpc.RpcError as e:
            # Log locally but don't fail the task
            print(f"[FlowForge] Failed to update status: {e}")

    def log(
        self,
        message: str,
        level: Union[str, LogLevel] = LogLevel.INFO,
        **extra,
    ) -> None:
        """
        Send a log message.

        Args:
            message: Log message
            level: Log level (DEBUG, INFO, WARN, ERROR)
            **extra: Additional structured fields

        Example:
            client.log("Processing file", level="INFO", filename="data.csv", size=1024)
        """
        if isinstance(level, LogLevel):
            level = level.value

        log_entry = {
            "timestamp": datetime.utcnow().isoformat() + "Z",
            "task_id": self.config.task_id,
            "workflow_id": self.config.workflow_id,
            "tenant_id": self.config.tenant_id,
            "level": level,
            "message": message,
            **extra,
        }

        self._log_queue.put(log_entry)

    def metric(
        self,
        name: str,
        value: Union[int, float],
        metric_type: Union[str, MetricType] = MetricType.GAUGE,
        tags: Optional[Dict[str, str]] = None,
    ) -> None:
        """
        Emit a metric.

        Args:
            name: Metric name
            value: Metric value
            metric_type: Type of metric (gauge, counter, histogram)
            tags: Optional metric tags

        Example:
            client.metric("items_processed", 100, tags={"batch": "1"})
            client.metric("processing_time_ms", 1523.5, metric_type="histogram")
        """
        if isinstance(metric_type, MetricType):
            metric_type = metric_type.value

        metric_entry = {
            "timestamp": int(time.time() * 1000),
            "task_id": self.config.task_id,
            "workflow_id": self.config.workflow_id,
            "tenant_id": self.config.tenant_id,
            "name": name,
            "value": value,
            "type": metric_type,
            "tags": tags or {},
        }

        self._metric_queue.put(metric_entry)

    def output(self, key: str, value: Any) -> None:
        """
        Store a task output that can be used by downstream tasks.

        Args:
            key: Output key name
            value: Output value (will be JSON serialized)

        Example:
            client.output("processed_file", "/data/output.parquet")
            client.output("stats", {"rows": 1000, "columns": 50})
        """
        request = flowforge_pb2.SetOutputRequest(
            task_id=self.config.task_id,
            workflow_id=self.config.workflow_id,
            key=key,
            value=json.dumps(value),
        )

        try:
            self._status_stub.SetOutput(request, timeout=5)
        except grpc.RpcError as e:
            print(f"[FlowForge] Failed to set output: {e}")

    def checkpoint(self, data: Dict[str, Any]) -> None:
        """
        Save a checkpoint for task recovery.

        Args:
            data: Checkpoint data (must be JSON serializable)

        Example:
            client.checkpoint({"last_processed_id": 12345, "batch": 5})
        """
        request = flowforge_pb2.SaveCheckpointRequest(
            task_id=self.config.task_id,
            workflow_id=self.config.workflow_id,
            data=json.dumps(data),
            timestamp=int(time.time() * 1000),
        )

        try:
            self._status_stub.SaveCheckpoint(request, timeout=5)
        except grpc.RpcError as e:
            print(f"[FlowForge] Failed to save checkpoint: {e}")

    def get_checkpoint(self) -> Optional[Dict[str, Any]]:
        """
        Retrieve the last checkpoint for this task.

        Returns:
            Checkpoint data or None if no checkpoint exists

        Example:
            checkpoint = client.get_checkpoint()
            if checkpoint:
                start_from = checkpoint["last_processed_id"]
        """
        request = flowforge_pb2.GetCheckpointRequest(
            task_id=self.config.task_id,
            workflow_id=self.config.workflow_id,
        )

        try:
            response = self._status_stub.GetCheckpoint(request, timeout=5)
            if response.data:
                return json.loads(response.data)
        except grpc.RpcError:
            pass

        return None

    # ==================== Background Workers ====================

    def _heartbeat_worker(self):
        """Send periodic heartbeats to indicate task is alive."""
        key = f"ff:heartbeat:{self.config.task_id}"

        while not self._shutdown.is_set():
            try:
                self._redis.zadd("ff:heartbeat", {self.config.task_id: time.time()})
                self._redis.expire(key, 60)  # Auto-expire if not renewed
            except redis.RedisError as e:
                print(f"[FlowForge] Heartbeat failed: {e}")

            self._shutdown.wait(self.config.heartbeat_interval)

    def _log_worker(self):
        """Batch and send logs."""
        batch = []
        last_flush = time.time()

        while not self._shutdown.is_set():
            try:
                entry = self._log_queue.get(timeout=0.1)
                batch.append(entry)
            except Empty:
                pass

            # Flush on batch size or interval
            should_flush = len(batch) >= self.config.log_batch_size or (
                batch and time.time() - last_flush >= self.config.log_flush_interval
            )

            if should_flush and batch:
                self._flush_logs(batch)
                batch = []
                last_flush = time.time()

        # Final flush on shutdown
        if batch:
            self._flush_logs(batch)

    def _flush_logs(self, batch: list):
        """Send log batch to log aggregator."""
        try:
            self._http_session.post(
                f"{self.config.log_endpoint}/v1/logs",
                json={"logs": batch},
                timeout=10,
            )
        except requests.RequestException as e:
            print(f"[FlowForge] Failed to flush logs: {e}")

    def _metric_worker(self):
        """Batch and send metrics."""
        batch = []
        last_flush = time.time()

        while not self._shutdown.is_set():
            try:
                entry = self._metric_queue.get(timeout=0.1)
                batch.append(entry)
            except Empty:
                pass

            if batch and time.time() - last_flush >= self.config.metric_flush_interval:
                self._flush_metrics(batch)
                batch = []
                last_flush = time.time()

        if batch:
            self._flush_metrics(batch)

    def _flush_metrics(self, batch: list):
        """Send metric batch to metrics collector."""
        try:
            self._http_session.post(
                f"{self.config.log_endpoint}/v1/metrics",
                json={"metrics": batch},
                timeout=10,
            )
        except requests.RequestException as e:
            print(f"[FlowForge] Failed to flush metrics: {e}")

    def close(self):
        """Gracefully shutdown the client."""
        self._shutdown.set()

        # Wait for workers to finish
        self._heartbeat_thread.join(timeout=2)
        self._log_thread.join(timeout=5)
        self._metric_thread.join(timeout=5)

        # Close connections
        self._grpc_channel.close()
        self._redis.close()
        self._http_session.close()
