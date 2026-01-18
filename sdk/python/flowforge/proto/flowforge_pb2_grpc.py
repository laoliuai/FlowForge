"""gRPC stubs for FlowForge TaskService."""

import grpc

from . import flowforge_pb2


class TaskServiceStub:
    """Client stub for TaskService."""

    def __init__(self, channel: grpc.Channel):
        self.UpdateStatus = channel.unary_unary(
            "/flowforge.v1.TaskService/UpdateStatus",
            request_serializer=flowforge_pb2._serialize,
            response_deserializer=flowforge_pb2.update_status_response_deserializer,
        )
        self.SetOutput = channel.unary_unary(
            "/flowforge.v1.TaskService/SetOutput",
            request_serializer=flowforge_pb2._serialize,
            response_deserializer=flowforge_pb2.set_output_response_deserializer,
        )
        self.SaveCheckpoint = channel.unary_unary(
            "/flowforge.v1.TaskService/SaveCheckpoint",
            request_serializer=flowforge_pb2._serialize,
            response_deserializer=flowforge_pb2.save_checkpoint_response_deserializer,
        )
        self.GetCheckpoint = channel.unary_unary(
            "/flowforge.v1.TaskService/GetCheckpoint",
            request_serializer=flowforge_pb2._serialize,
            response_deserializer=flowforge_pb2.get_checkpoint_response_deserializer,
        )
        self.StreamLogs = channel.stream_unary(
            "/flowforge.v1.TaskService/StreamLogs",
            request_serializer=flowforge_pb2._serialize,
            response_deserializer=flowforge_pb2.stream_logs_response_deserializer,
        )


class TaskServiceServicer:
    """Server API for TaskService."""

    def UpdateStatus(self, request, context):
        raise NotImplementedError("UpdateStatus is not implemented")

    def SetOutput(self, request, context):
        raise NotImplementedError("SetOutput is not implemented")

    def SaveCheckpoint(self, request, context):
        raise NotImplementedError("SaveCheckpoint is not implemented")

    def GetCheckpoint(self, request, context):
        raise NotImplementedError("GetCheckpoint is not implemented")

    def StreamLogs(self, request_iterator, context):
        raise NotImplementedError("StreamLogs is not implemented")


def add_TaskServiceServicer_to_server(servicer, server):
    rpc_method_handlers = {
        "UpdateStatus": grpc.unary_unary_rpc_method_handler(
            servicer.UpdateStatus,
            request_deserializer=flowforge_pb2.update_status_request_deserializer,
            response_serializer=flowforge_pb2._serialize,
        ),
        "SetOutput": grpc.unary_unary_rpc_method_handler(
            servicer.SetOutput,
            request_deserializer=flowforge_pb2.set_output_request_deserializer,
            response_serializer=flowforge_pb2._serialize,
        ),
        "SaveCheckpoint": grpc.unary_unary_rpc_method_handler(
            servicer.SaveCheckpoint,
            request_deserializer=flowforge_pb2.save_checkpoint_request_deserializer,
            response_serializer=flowforge_pb2._serialize,
        ),
        "GetCheckpoint": grpc.unary_unary_rpc_method_handler(
            servicer.GetCheckpoint,
            request_deserializer=flowforge_pb2.get_checkpoint_request_deserializer,
            response_serializer=flowforge_pb2._serialize,
        ),
        "StreamLogs": grpc.stream_unary_rpc_method_handler(
            servicer.StreamLogs,
            request_deserializer=flowforge_pb2.log_entry_deserializer,
            response_serializer=flowforge_pb2._serialize,
        ),
    }
    generic_handler = grpc.method_handlers_generic_handler(
        "flowforge.v1.TaskService", rpc_method_handlers
    )
    server.add_generic_rpc_handlers((generic_handler,))


__all__ = ["TaskServiceStub", "TaskServiceServicer", "add_TaskServiceServicer_to_server"]
