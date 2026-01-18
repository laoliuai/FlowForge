"""Lightweight protobuf-like message definitions for FlowForge SDK."""

import json
from dataclasses import dataclass
from typing import Any, Dict, Optional


class _ProtoMessage:
    def SerializeToString(self) -> bytes:
        return json.dumps(self.__dict__).encode("utf-8")

    @classmethod
    def FromString(cls, data: bytes):
        if not data:
            return cls()
        payload = json.loads(data.decode("utf-8"))
        return cls(**payload)


@dataclass
class UpdateStatusRequest(_ProtoMessage):
    task_id: str = ""
    workflow_id: str = ""
    status: str = ""
    message: str = ""
    progress: int = 0
    details: str = ""
    timestamp: int = 0


@dataclass
class UpdateStatusResponse(_ProtoMessage):
    success: bool = False


@dataclass
class SetOutputRequest(_ProtoMessage):
    task_id: str = ""
    workflow_id: str = ""
    key: str = ""
    value: str = ""


@dataclass
class SetOutputResponse(_ProtoMessage):
    success: bool = False


@dataclass
class SaveCheckpointRequest(_ProtoMessage):
    task_id: str = ""
    workflow_id: str = ""
    data: str = ""
    timestamp: int = 0


@dataclass
class SaveCheckpointResponse(_ProtoMessage):
    success: bool = False


@dataclass
class GetCheckpointRequest(_ProtoMessage):
    task_id: str = ""
    workflow_id: str = ""


@dataclass
class GetCheckpointResponse(_ProtoMessage):
    data: str = ""
    timestamp: int = 0


@dataclass
class LogEntry(_ProtoMessage):
    task_id: str = ""
    workflow_id: str = ""
    tenant_id: str = ""
    timestamp: int = 0
    level: str = ""
    message: str = ""
    extra: str = ""


@dataclass
class StreamLogsResponse(_ProtoMessage):
    received: int = 0
    processed: int = 0


def _serialize(message: _ProtoMessage) -> bytes:
    return message.SerializeToString()


def _deserialize(message_cls, data: bytes):
    return message_cls.FromString(data)


def update_status_request_deserializer(data: bytes) -> UpdateStatusRequest:
    return _deserialize(UpdateStatusRequest, data)


def update_status_response_deserializer(data: bytes) -> UpdateStatusResponse:
    return _deserialize(UpdateStatusResponse, data)


def set_output_request_deserializer(data: bytes) -> SetOutputRequest:
    return _deserialize(SetOutputRequest, data)


def set_output_response_deserializer(data: bytes) -> SetOutputResponse:
    return _deserialize(SetOutputResponse, data)


def save_checkpoint_request_deserializer(data: bytes) -> SaveCheckpointRequest:
    return _deserialize(SaveCheckpointRequest, data)


def save_checkpoint_response_deserializer(data: bytes) -> SaveCheckpointResponse:
    return _deserialize(SaveCheckpointResponse, data)


def get_checkpoint_request_deserializer(data: bytes) -> GetCheckpointRequest:
    return _deserialize(GetCheckpointRequest, data)


def get_checkpoint_response_deserializer(data: bytes) -> GetCheckpointResponse:
    return _deserialize(GetCheckpointResponse, data)


def log_entry_deserializer(data: bytes) -> LogEntry:
    return _deserialize(LogEntry, data)


def stream_logs_response_deserializer(data: bytes) -> StreamLogsResponse:
    return _deserialize(StreamLogsResponse, data)


__all__ = [
    "UpdateStatusRequest",
    "UpdateStatusResponse",
    "SetOutputRequest",
    "SetOutputResponse",
    "SaveCheckpointRequest",
    "SaveCheckpointResponse",
    "GetCheckpointRequest",
    "GetCheckpointResponse",
    "LogEntry",
    "StreamLogsResponse",
    "_serialize",
    "update_status_request_deserializer",
    "update_status_response_deserializer",
    "set_output_request_deserializer",
    "set_output_response_deserializer",
    "save_checkpoint_request_deserializer",
    "save_checkpoint_response_deserializer",
    "get_checkpoint_request_deserializer",
    "get_checkpoint_response_deserializer",
    "log_entry_deserializer",
    "stream_logs_response_deserializer",
]
