"""Offline check: the agent is blocked first, then recovers in the order the barrier names.

  JOLTRIN_MCP_SERVER=./sop-mcp-server python examples/langgraph_agent/test_agent.py
"""
import asyncio
import os

from agent import GOAL, run

steps = asyncio.run(run(os.environ.get("JOLTRIN_MCP_SERVER", "sop-mcp-server")))

assert steps[0] == (GOAL, False), f"the goal should be blocked first, got {steps[0]}"
done = [s for s, ok in steps if ok]
assert done == ["take_backup", "validate_backup", GOAL], f"wrong order of executed steps: {done}"
print("ok:", steps)
