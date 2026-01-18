from enum import Enum


class TaskStatus(Enum):
    """Task execution status."""

    PENDING = "PENDING"
    QUEUED = "QUEUED"
    RUNNING = "RUNNING"
    SUCCEEDED = "SUCCEEDED"
    FAILED = "FAILED"
    CANCELLED = "CANCELLED"
    RETRYING = "RETRYING"
    SKIPPED = "SKIPPED"


class LogLevel(Enum):
    """Log severity levels."""

    DEBUG = "DEBUG"
    INFO = "INFO"
    WARN = "WARN"
    ERROR = "ERROR"


class MetricType(Enum):
    """Metric types."""

    GAUGE = "gauge"  # Point-in-time value
    COUNTER = "counter"  # Monotonically increasing
    HISTOGRAM = "histogram"  # Distribution of values
