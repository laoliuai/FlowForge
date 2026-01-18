"""Simple DAG workflow demo integrated with the FlowForge SDK.

This demo builds a two-node DAG:
1. A suffix node that appends a suffix to an input string.
2. A print node that prints the resulting string to stdout.
"""

from dataclasses import dataclass
from typing import Callable, Dict, Optional

from flowforge.client import FlowForgeClient


@dataclass
class DemoNode:
    name: str
    run: Callable[[str, FlowForgeClient], str]
    next_node: Optional[str] = None


def suffix_node(value: str, client: FlowForgeClient) -> str:
    updated = f"{value}-suffix"
    client.log("Suffix node updated value", node="suffix", value=updated)
    client.output("suffix_output", {"value": updated})
    return updated


def print_node(value: str, client: FlowForgeClient) -> str:
    print(value)
    client.log("Print node emitted value", node="print", value=value)
    client.output("printed_value", {"value": value})
    return value


def build_demo_dag() -> Dict[str, DemoNode]:
    return {
        "suffix": DemoNode(name="suffix", run=suffix_node, next_node="print"),
        "print": DemoNode(name="print", run=print_node),
    }


def get_client() -> FlowForgeClient:
    try:
        return FlowForgeClient.from_env()
    except KeyError:
        raise RuntimeError(
            "FlowForge environment variables are required to run this demo. "
            "Set FLOWFORGE_* variables and rerun."
        )


def run_demo(initial_value: str) -> str:
    dag = build_demo_dag()
    client = get_client()

    current_value = initial_value
    current_node = dag["suffix"]

    while current_node:
        current_value = current_node.run(current_value, client)
        current_node = dag.get(current_node.next_node)

    return current_value


if __name__ == "__main__":
    run_demo("flowforge")
