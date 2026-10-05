# What is Joltrin, in plain words

**Joltrin stores your application's data and keeps it correct, without you running a database server.**

## The problem

Most software needs three things: a place to keep data, a guarantee the data never ends up half-written, and a way to grow from one machine to many. Today that usually means operating a separate database (Postgres, Cassandra, etc.), plus caching, plus replication. Each piece costs money and people.

## What Joltrin does instead

Joltrin is a library you compile into your program. Your app gets:

1. **A sorted filing cabinet (B-Tree).** Data stays in order, so "find this record" and "give me everything between A and B" are both fast.
2. **All-or-nothing saves (ACID transactions).** A crash mid-write never leaves half-updated data.
3. **Fault tolerance without full copies (erasure coding).** Traditional systems keep 3 full copies of your data. Joltrin stores math-derived fragments that can rebuild lost pieces, using roughly half the disk.
4. **Grow-as-you-go (swarm computing).** The same program that runs on one laptop can join a cluster and share the work. No rewrite.
5. **Use it from your language.** The engine is written in Go; Python and C# packages are published. Java and Rust bindings are in the repo and not published yet.

## Who it's for

- Teams that want database guarantees without a database bill.
- Products that start small (one device, one server) but must scale later.
- Workloads with lots of ordered data: time series, catalogs, logs, AI datasets.

## How it ships

Every change goes through an automated pipeline: build, tests, packaging into a container, a staging check, and a human approval before production. See [SDLC.md](SDLC.md) and [RELEASE_PROCESS.md](RELEASE_PROCESS.md).

## Where to go next

- Try it in 10 seconds: `go run ./examples/quickstart`
- [Getting started](GETTING_STARTED.md)
- [Architecture whitepaper](SOP_ARCHITECTURE_WHITEPAPER.md), if you want the deep end
