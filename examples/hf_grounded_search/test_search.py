"""Checks the search against questions whose answers are known, and prints the scores.

  PYTHONPATH=bindings/python python examples/hf_grounded_search/test_search.py

It asserts three things:
  - the score the Joltrin store returns is the cosine similarity of the two vectors,
  - each in-domain question finds its source file in the top 3,
  - each question about something else gets "I don't know".
The last group is probes: questions near the docs' topic that the docs do not answer.
They are printed, not asserted, because a score threshold cannot tell "close to the
topic" from "answered by the docs", and the output shows where that breaks.
"""
import tempfile

import torch

import search

IN_DOMAIN = [
    ("How long are remembered blocks kept before they stop applying?", {"AGENT_PROTOCOLS.md"}),
    ("Which state must hold before a node can be drained?", {"AGENT_PROTOCOLS.md"}),
    ("What do I set to serve my own runbooks instead of the example?", {"AGENT_PROTOCOLS.md"}),
    ("Is the precedence check full LTL or CTL model checking?", {"AGENT_PROTOCOLS.md", "MCP_A2A_AND_VERIFICATION_ENGINE.md"}),
    ("Who is this for if I run a platform or SRE team?", {"WHO_IS_IT_FOR.md"}),
    ("Did the agents act on the feedback in a block?", {"AGENT_BARRIER_TESTS.md"}),
    ("How do I add the library to a Go project?", {"README.md"}),
    ("How do I register the server with Claude Code, Codex and the Gemini CLI?", {"AGENT_PROTOCOLS.md"}),
]
OUT_OF_DOMAIN = [
    "What is the capital of France?",
    "How do I bake sourdough bread?",
    "Which graphics card should I buy for gaming?",
    "What is the weather in San Diego today?",
    "Explain how photosynthesis works.",
    "Who won the 2018 football World Cup?",
]
NEAR_DOMAIN_PROBES = [
    "How do I configure PostgreSQL streaming replication?",
    "How do I write a Kubernetes liveness probe?",
    "What is the difference between TCP and UDP?",
]

embed = search.Embedder()
tmp = tempfile.TemporaryDirectory(prefix="joltrin-hf-test-")  # removed when the script exits
index = search.Index(tmp.name)
n = index.build(embed)
print(f"indexed {n} chunks with {search.MODEL}@{search.REVISION[:7]}")

# 1. The store's score is the cosine similarity. Vectors have length 1, so that is a dot product.
question = "What do I set to serve my own runbooks instead of the example?"
q = embed([question])
hits, best = index.ask(embed, question, k=1, min_score=0.0)
chunk_vec = torch.tensor(embed([hits[0].payload["text"]])[0])
cosine = float(torch.dot(torch.tensor(q[0]), chunk_vec))
assert abs(cosine - hits[0].score) < 0.01, f"store score {hits[0].score} is not the cosine {cosine}"
print(f"store score {hits[0].score:.4f} matches cosine {cosine:.4f}")

# 2. In-domain questions find their source in the top 3.
low_in = 1.0
for question, sources in IN_DOMAIN:
    hits, best = index.ask(embed, question, k=3, min_score=0.0)
    found = [h.payload["source"].split("#")[0].split("/")[-1] for h in hits]
    low_in = min(low_in, best)
    print(f"  in   {best:.2f}  top3={found}  {question}")
    assert sources & set(found), f"{question!r} did not find {sources} in {found}"

# 3. Questions about something else are refused, with room to spare.
high_out = 0.0
for question in OUT_OF_DOMAIN:
    hits, best = index.ask(embed, question)
    high_out = max(high_out, best)
    print(f"  out  {best:.2f}  {'answered' if hits else 'refused '}  {question}")
    assert not hits, f"{question!r} should be refused, best score {best:.2f}"

print(f"\nlowest in-domain best score {low_in:.2f}, highest out-of-domain {high_out:.2f}, threshold {search.MIN_SCORE:.2f}")
assert high_out < search.MIN_SCORE < low_in, "the threshold no longer separates the two groups"

# Probes: printed only.
for question in NEAR_DOMAIN_PROBES:
    hits, best = index.ask(embed, question)
    print(f"  near {best:.2f}  {'ANSWERED' if hits else 'refused '}  {question}")
print("ok")
