import json
import os
from queue import Empty
from unittest import TestCase, mock

from flowforge.client import Config, FlowForgeClient


class FlowForgeClientTests(TestCase):
    def _build_client(self, config: Config) -> FlowForgeClient:
        with mock.patch("atexit.register"), \
            mock.patch.object(FlowForgeClient, "_init_grpc"), \
            mock.patch.object(FlowForgeClient, "_init_redis"), \
            mock.patch.object(FlowForgeClient, "_init_http_session"), \
            mock.patch.object(FlowForgeClient, "_start_workers"):
            client = FlowForgeClient(config)

        client._grpc_channel = mock.Mock()
        client._redis = mock.Mock()
        client._http_session = mock.Mock()
        client._heartbeat_thread = mock.Mock()
        client._log_thread = mock.Mock()
        client._metric_thread = mock.Mock()
        client._status_stub = mock.Mock()
        return client

    def test_from_env_loads_config(self):
        env = {
            "FLOWFORGE_TASK_ID": "task-1",
            "FLOWFORGE_WORKFLOW_ID": "workflow-1",
            "FLOWFORGE_TENANT_ID": "tenant-1",
            "FLOWFORGE_PROJECT_ID": "project-1",
            "FLOWFORGE_API_ENDPOINT": "localhost:9090",
            "FLOWFORGE_REDIS_ENDPOINT": "localhost:6379",
            "FLOWFORGE_LOG_ENDPOINT": "http://localhost:8081",
            "FLOWFORGE_AUTH_TOKEN": "token",
            "FLOWFORGE_HEARTBEAT_INTERVAL": "9",
        }

        with mock.patch.dict(os.environ, env, clear=True), \
            mock.patch("atexit.register"), \
            mock.patch.object(FlowForgeClient, "_init_grpc"), \
            mock.patch.object(FlowForgeClient, "_init_redis"), \
            mock.patch.object(FlowForgeClient, "_init_http_session"), \
            mock.patch.object(FlowForgeClient, "_start_workers"):
            client = FlowForgeClient.from_env()

        self.assertEqual(client.config.task_id, "task-1")
        self.assertEqual(client.config.workflow_id, "workflow-1")
        self.assertEqual(client.config.heartbeat_interval, 9)

    def test_log_enqueues_entry(self):
        config = Config(
            task_id="task-1",
            workflow_id="workflow-1",
            tenant_id="tenant-1",
            project_id="project-1",
            api_endpoint="localhost:9090",
            redis_endpoint="localhost:6379",
            log_endpoint="http://localhost:8081",
            auth_token="token",
        )

        client = self._build_client(config)
        client.log("hello", level="INFO", run="demo")

        try:
            entry = client._log_queue.get_nowait()
        except Empty as exc:
            raise AssertionError("expected log entry in queue") from exc

        self.assertEqual(entry["message"], "hello")
        self.assertEqual(entry["level"], "INFO")
        self.assertEqual(entry["run"], "demo")
        self.assertEqual(entry["task_id"], "task-1")

    def test_metric_enqueues_entry(self):
        config = Config(
            task_id="task-1",
            workflow_id="workflow-1",
            tenant_id="tenant-1",
            project_id="project-1",
            api_endpoint="localhost:9090",
            redis_endpoint="localhost:6379",
            log_endpoint="http://localhost:8081",
            auth_token="token",
        )

        client = self._build_client(config)
        client.metric("items_processed", 12, tags={"batch": "a"})

        try:
            entry = client._metric_queue.get_nowait()
        except Empty as exc:
            raise AssertionError("expected metric entry in queue") from exc

        self.assertEqual(entry["name"], "items_processed")
        self.assertEqual(entry["value"], 12)
        self.assertEqual(entry["tags"], {"batch": "a"})

    def test_output_sends_request(self):
        config = Config(
            task_id="task-1",
            workflow_id="workflow-1",
            tenant_id="tenant-1",
            project_id="project-1",
            api_endpoint="localhost:9090",
            redis_endpoint="localhost:6379",
            log_endpoint="http://localhost:8081",
            auth_token="token",
        )

        client = self._build_client(config)
        client.output("result", {"ok": True})

        client._status_stub.SetOutput.assert_called_once()
        request = client._status_stub.SetOutput.call_args[0][0]

        self.assertEqual(request.task_id, "task-1")
        self.assertEqual(request.workflow_id, "workflow-1")
        self.assertEqual(request.key, "result")
        self.assertEqual(json.loads(request.value), {"ok": True})
