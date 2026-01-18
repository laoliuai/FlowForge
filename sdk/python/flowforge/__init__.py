"""
FlowForge Python SDK

Usage:
    from flowforge import client

    # Automatic initialization from environment
    client.log("Processing started")
    client.metric("items_processed", 100)
    client.status("RUNNING", progress=50)
    client.output("result", {"key": "value"})
"""

from .client import FlowForgeClient
from .types import TaskStatus, LogLevel, MetricType

# Singleton client instance (auto-configured from env)
client: FlowForgeClient = None


def _init_client():
    global client
    if client is None:
        client = FlowForgeClient.from_env()
    return client


# Auto-initialize on import
_init_client()

__all__ = ["client", "FlowForgeClient", "TaskStatus", "LogLevel", "MetricType"]
