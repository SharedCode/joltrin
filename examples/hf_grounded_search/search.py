"""Answer questions from Joltrin's own docs, with citations, or say "I don't know".

What it does, end to end:

  1. Split the docs into chunks, one citation (file#heading) per chunk.
  2. Turn each chunk into a 384-number vector with a Hugging Face model.
  3. Store the vectors in a Joltrin vector store, inside a transaction.
  4. For a question: embed it, ask the store for the nearest chunks, and
     return them with their citations. If even the best match is not close
     enough, return nothing and say so.

Step 2 is written with transformers and torch directly, not with the
sentence-transformers wrapper, so each part is visible: tokenize, run the
model, average the token vectors while ignoring padding, normalize to length 1.

This does retrieval only. Nothing here writes new text, so nothing here can
invent a sentence. The passages are the docs' own words. Giving them to a
language model as context is a separate step this example does not take.

Run it from the repo root, after building the native library for your machine
(see bindings/python/README.md) and installing requirements.txt:

  PYTHONPATH=bindings/python python examples/hf_grounded_search/search.py "how long do lessons last?"
"""
import argparse
import re
import sys
import tempfile
from pathlib import Path

import torch
import torch.nn.functional as F
from transformers import AutoModel, AutoTokenizer

from sop import Context
from sop.ai import Database, DatabaseType, Item
from sop.database import DatabaseOptions

REPO = Path(__file__).resolve().parents[2]
CORPUS = ["README.md", "docs/AGENT_PROTOCOLS.md", "docs/AGENT_BARRIER_TESTS.md",
          "docs/MCP_A2A_AND_VERIFICATION_ENGINE.md", "docs/WHO_IS_IT_FOR.md"]

# Pinned to one commit, so a changed model on the Hub cannot change the results.
MODEL = "sentence-transformers/all-MiniLM-L6-v2"
REVISION = "1110a243fdf4706b3f48f1d95db1a4f5529b4d41"

CHUNK_WORDS = 150
# A match scoring below this is treated as "not in the docs". It sits between the
# highest score of the questions about other topics (0.17) and the lowest score of
# the questions the docs answer (0.30) in test_search.py. That is 14 questions, so
# treat it as a starting point and re-measure on your own questions.
MIN_SCORE = 0.25


def chunks(root: Path = REPO, files=CORPUS):
    """Yield (citation, text). A chunk never crosses a heading."""
    for name in files:
        heading, buf = "top", []

        def flush():
            words = " ".join(buf).split()
            for i in range(0, len(words), CHUNK_WORDS):
                piece = " ".join(words[i:i + CHUNK_WORDS])
                if len(piece.split()) >= 8:
                    yield f"{name}#{heading}", piece

        in_code = False
        for line in (root / name).read_text().splitlines():
            if line.startswith("```"):
                in_code = not in_code
            if not in_code and re.match(r"#{1,3} ", line):
                yield from flush()
                heading, buf = line.lstrip("# ").strip(), []
            else:
                buf.append(line)
        yield from flush()


class Embedder:
    def __init__(self):
        self.tok = AutoTokenizer.from_pretrained(MODEL, revision=REVISION)
        self.model = AutoModel.from_pretrained(MODEL, revision=REVISION).eval()

    @torch.no_grad()
    def __call__(self, texts, batch=32):
        out = []
        for i in range(0, len(texts), batch):
            # 1. Text to token ids. Short texts are padded so a batch is one tensor.
            enc = self.tok(texts[i:i + batch], padding=True, truncation=True, max_length=256, return_tensors="pt")
            # 2. One 384-number vector per token: shape (batch, tokens, 384).
            tokens = self.model(**enc).last_hidden_state
            # 3. Average the token vectors, but not the padding. The mask is 1 for
            #    real tokens and 0 for padding.
            mask = enc["attention_mask"].unsqueeze(-1).float()
            pooled = (tokens * mask).sum(dim=1) / mask.sum(dim=1).clamp(min=1e-9)
            # 4. Length 1, so the store's similarity is the cosine of the angle.
            out.extend(F.normalize(pooled, dim=1).tolist())
        return out


class Index:
    def __init__(self, folder: str):
        self.ctx = Context()
        self.db = Database(DatabaseOptions(stores_folders=[folder], type=DatabaseType.Standalone))

    def build(self, embed: Embedder, root: Path = REPO, files=CORPUS) -> int:
        rows = list(chunks(root, files))
        vectors = embed([text for _, text in rows])
        items = [Item(id=f"{i}", vector=v, payload={"source": src, "text": text})
                 for i, ((src, text), v) in enumerate(zip(rows, vectors))]
        tx = self.db.begin_transaction(self.ctx)  # all chunks land together or not at all
        self.db.open_vector_store(self.ctx, tx, "docs").upsert_batch(self.ctx, items)
        tx.commit(self.ctx)
        return len(items)

    def ask(self, embed: Embedder, question: str, k: int = 3, min_score: float = MIN_SCORE):
        """The k nearest chunks, best first, or [] when the best one is below min_score."""
        tx = self.db.begin_transaction(self.ctx)
        hits = self.db.open_vector_store(self.ctx, tx, "docs").query(self.ctx, vector=embed([question])[0], k=k)
        tx.commit(self.ctx)
        if not hits or hits[0].score < min_score:
            return [], (hits[0].score if hits else 0.0)
        return hits, hits[0].score


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("question")
    ap.add_argument("-k", type=int, default=3)
    ap.add_argument("--min-score", type=float, default=MIN_SCORE)
    args = ap.parse_args()

    embed = Embedder()
    with tempfile.TemporaryDirectory(prefix="joltrin-hf-") as tmp:
        index = Index(tmp)
        n = index.build(embed)
        hits, best = index.ask(embed, args.question, args.k, args.min_score)
    if not hits:
        print(f"I don't know. Nothing in {n} indexed passages is close enough (best score {best:.2f}, needs {args.min_score:.2f}).")
        return 1
    for h in hits:
        print(f"[{h.score:.2f}] {h.payload['source']}\n    {h.payload['text'][:400]}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
