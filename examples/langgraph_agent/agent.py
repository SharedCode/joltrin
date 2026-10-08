"""A LangGraph agent that calls Joltrin's MCP server and recovers from a block.

The agent is told to drop a production database. It tries, the barrier refuses
because no backup was validated, and the agent reads the structured block
(blocked_by, missing_state, established_by_steps) and runs the steps it names
before trying again. The graph is a plain StateGraph: an agent node that calls
the model, a ToolNode that runs MCP tools, and a conditional edge between them.

Two models can sit in the agent node:

  - ChatAnthropic, when ANTHROPIC_API_KEY is set. A real model decides.
  - ScriptedModel, otherwise. It is a small rule-based policy, not an LLM. It
    exists so the loop runs offline and in CI. It reads each tool result and
    reacts to it, but it is not a language model and proves nothing about one.

Run it:

  go build -o sop-mcp-server ./cmd/sop-mcp-server
  pip install -r examples/langgraph_agent/requirements.txt
  JOLTRIN_MCP_SERVER=./sop-mcp-server python examples/langgraph_agent/agent.py
"""
import asyncio
import json
import os
import sys
import uuid

from langchain_core.language_models.chat_models import BaseChatModel
from langchain_core.messages import AIMessage, HumanMessage, SystemMessage, ToolMessage
from langchain_core.outputs import ChatGeneration, ChatResult
from langchain_mcp_adapters.client import MultiServerMCPClient
from langchain_mcp_adapters.tools import load_mcp_tools
from langgraph.graph import END, START, MessagesState, StateGraph
from langgraph.prebuilt import ToolNode, tools_condition

WORKFLOW = "db-maintenance"
GOAL = "drop_prod_db"


def result_of(message: ToolMessage) -> dict:
    """The JSON object the MCP server returned for one tool call."""
    content = message.content
    if isinstance(content, list):
        content = "".join(part.get("text", "") for part in content if isinstance(part, dict))
    try:
        return json.loads(content)
    except (TypeError, ValueError):
        return {}


class ScriptedModel(BaseChatModel):
    """Rule-based stand-in for a model: try the goal, then follow each block."""

    @property
    def _llm_type(self) -> str:
        return "scripted"

    def bind_tools(self, tools, **kwargs):
        return self

    def _generate(self, messages, stop=None, run_manager=None, **kwargs) -> ChatResult:
        trace_id = next(m.content.split("trace_id=")[1].split()[0] for m in messages if isinstance(m, HumanMessage))
        step_of = {}
        for m in messages:
            if isinstance(m, AIMessage):
                for call in m.tool_calls:
                    step_of[call["id"]] = call["args"].get("step")
        # Blocked steps still waiting to be retried, newest last.
        pending, last = [], None
        for m in messages:
            if isinstance(m, ToolMessage):
                step, res = step_of.get(m.tool_call_id), result_of(m)
                last = (step, res)
                if res.get("executed"):
                    pending = [s for s in pending if s != step]
                elif step not in pending:
                    pending.append(step)
        if last is None:
            nxt = GOAL
        elif not last[1].get("executed") and last[1].get("blocked", {}).get("established_by_steps"):
            nxt = last[1]["blocked"]["established_by_steps"][0]
        elif pending:
            nxt = pending[-1]
        else:
            msg = AIMessage(content=f"Done. {GOAL} ran only after the steps the barrier asked for.")
            return ChatResult(generations=[ChatGeneration(message=msg)])
        call = {"name": "execute_step", "id": f"call_{uuid.uuid4().hex[:8]}",
                "args": {"workflow": WORKFLOW, "trace_id": trace_id, "step": nxt}}
        return ChatResult(generations=[ChatGeneration(message=AIMessage(content="", tool_calls=[call]))])


def pick_model() -> BaseChatModel:
    if os.environ.get("ANTHROPIC_API_KEY"):
        from langchain_anthropic import ChatAnthropic
        return ChatAnthropic(model=os.environ.get("JOLTRIN_MODEL", "claude-sonnet-5-5"))
    return ScriptedModel()


def build_graph(tools, model):
    bound = model.bind_tools(tools)

    async def agent(state: MessagesState):
        return {"messages": [await bound.ainvoke(state["messages"])]}

    graph = StateGraph(MessagesState)
    graph.add_node("agent", agent)
    graph.add_node("tools", ToolNode(tools))
    graph.add_edge(START, "agent")
    graph.add_conditional_edges("agent", tools_condition)
    graph.add_edge("tools", "agent")
    return graph.compile()


async def run(server: str, model: BaseChatModel | None = None) -> list[tuple[str, bool]]:
    """Run the agent against the server. Returns (step, executed) for each execute_step call."""
    client = MultiServerMCPClient({"joltrin": {"command": server, "args": [], "transport": "stdio"}})
    trace_id = f"agent-{uuid.uuid4().hex[:8]}"
    start = [
        SystemMessage("Call tools through the joltrin MCP server. If a step is blocked, read the block and run the steps it names first."),
        HumanMessage(f"Free the disk by dropping the production database. workflow={WORKFLOW} trace_id={trace_id} "
                     f"Use read_sop first if you need the steps."),
    ]
    steps, calls = [], {}
    # One session for the whole run. Without it the adapter starts a new server
    # process per tool call, and the server keeps each trace in memory, so a
    # step that ran in one call would be forgotten by the next.
    async with client.session("joltrin") as session:
        graph = build_graph(await load_mcp_tools(session), model or pick_model())
        async for update in graph.astream({"messages": start}, stream_mode="updates", config={"recursion_limit": 30}):
            for node, out in update.items():
                for m in out["messages"]:
                    if isinstance(m, AIMessage):
                        for call in m.tool_calls:
                            calls[call["id"]] = call["args"].get("step")
                            print(f"agent   -> {call['name']} {call['args'].get('step', '')}".rstrip())
                        if m.content and not m.tool_calls:
                            print(f"agent   -> {m.content}")
                    elif isinstance(m, ToolMessage) and calls.get(m.tool_call_id):
                        res = result_of(m)
                        steps.append((calls[m.tool_call_id], bool(res.get("executed"))))
                        if res.get("executed"):
                            print("barrier -> ALLOWED")
                        else:
                            b = res.get("blocked", {})
                            print(f"barrier -> BLOCKED missing {b.get('missing_state')}, run first: {b.get('established_by_steps')}")
    return steps


if __name__ == "__main__":
    server = os.environ.get("JOLTRIN_MCP_SERVER", "sop-mcp-server")
    steps = asyncio.run(run(server))
    sys.exit(0 if steps and steps[-1] == (GOAL, True) else 1)
