# Joltrin Arena - Distributed Systems Survival Simulation & Architecture Demo

[![Deploy to GitHub Pages](https://github.com/sharedcode/joltrin/actions/workflows/deploy-demo.yml/badge.svg)](https://sharedcode.github.io/joltrin/arena/)
[![Go Version](https://img.shields.io/badge/Engine-Go_/_WASM-00ADD8?logo=go)](https://github.com/sharedcode/joltrin)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](../LICENSE)

> **"Break the system. Watch Joltrin recover. Experience one engine for data and compute."**

[**Live Interactive Experience: sharedcode.github.io/joltrin/arena**](https://sharedcode.github.io/joltrin/arena/)

---

## 🎯 The Vision: "Show Me Why I Should Care"

The traditional approach to explaining distributed storage and compute is writing lengthy technical documentation. **Joltrin Arena** turns abstract technical features (ACID transactions, B-Tree storage, erasure coding, swarm computing, and failover coordination) into an **interactive 2-3 minute survival simulation**.

Instead of telling investors and CTOs that Joltrin simplifies infrastructure, **Joltrin Arena lets them break the system and watch it heal in real-time**.

```
WITHOUT JOLTRIN (Current Multi-Component Tax):
Database -> Queue -> Workers -> Distributed Locks -> Retries -> ZooKeeper -> Failover Glue
(4-6 separate failure domains | 15-50ms latency | 60% engineering time on glue code)

WITH JOLTRIN (Unified Engine):
Application -> Joltrin
(1 Single Engine | Sub-millisecond execution | 100% ACID consistency | Automatic failover)
```

---

## 🎮 Core Interactive Features

### 1. ⚡ "The 60-Second Disaster" (Scripted 'Aha!' Scenario)
A fully automated, high-intensity simulation demonstrating resilience:
1. **Baseline Load**: 10,000 TPS steady state.
2. **Traffic Surge**: Ingestion spikes to 45,000 TPS.
3. **Hardware Fault**: Storage Shard #02 crashes offline mid-transaction.
4. **Swarm Crash**: Agent Worker 03 terminates abruptly.
5. **Transaction Storm**: Concurrent OCC lock contention resolved in `<0.5ms`.
6. **Erasure Rebuild**: Reed-Solomon parity fragments reconstruct missing blocks.
7. **System Stabilized**: 100.00% consistency maintained with **0 dropped writes**.

### 2. 🤖 Flagship Scenario: "Real-Time AI Agent Workforce"
- Demonstrates **AI + Persistent State + Distributed Compute + Coordination**.
- 5,000 autonomous agent tasks processed in parallel.
- Agents crash mid-thought without corrupting memory context or losing uncommitted write frames.
- Context is handed off to surviving swarm workers in `<15ms`.

### 3. ⚖️ "With Joltrin vs. Without Joltrin" Interactive Split
- Visual comparison contrasting traditional fragmented architectures (Postgres + Kafka + Redis + ZooKeeper) with Joltrin's unified architecture.

### 4. 💼 Investor Mode (Interactive Pitch Deck)
- **The Problem**: The multi-component tax and glue code burden.
- **The Thesis**: "One engine for data and compute."
- **Commercial Use Cases**: Interactive cards for *AI Agent Workforces*, *Real-Time Multiplayer Games*, *Sub-Millisecond Fintech Ledgers*, *Edge/IoT Swarms*, and *Serverless Metadata*.

### 5. 🧠 Joltrin Copilot (Explainability Q&A)
- Contextual explainer answering:
  - *"Why did the node failure not corrupt the data?"*
  - *"How is this different from Postgres + Kafka + Redis?"*
  - *"Where does the ACID transaction actually happen?"*
  - *"What is the business ROI for a CTO?"*

### 6. 🔊 Procedural Web Audio Synthesizer
- Built using the native Web Audio API with zero external audio files. Synthesizes subtle clicks, commit chimes, fault alarms, and victory fanfares (with instant mute control).

---

## 🛠️ Tech Stack & Architecture

- **Frontend**: React 18 + TypeScript + Vite
- **Styling**: Tailwind CSS (Dark Obsidian & Emerald Telemetry theme)
- **Icons**: Lucide React + Custom SVG
- **Graphics**: Hardware-accelerated HTML5 2D Canvas rendering live bezier connections and particle physics at 60 FPS
- **Deployment**: Static single-page application optimized for GitHub Pages (`base: './'`)

---

## 🚀 Local Development

```bash
# 1. Clone the repository
git clone https://github.com/sharedcode/joltrin.git
cd joltrin/sop-arena

# 2. Install dependencies
npm install

# 3. Start development server
npm run dev

# 4. Build for production
npm run build
```

---

## 🌐 GitHub Pages Deployment

Joltrin Arena is not a standalone repository. It is built and deployed from inside the main `sharedcode/sop` repository by `.github/workflows/deploy-demo.yml`, which builds this app (`npm run build`) alongside the Go WASM technical demo and publishes both into one GitHub Pages site:

- Technical demo (WASM ACID transactions, vector search, agent memory): `https://sharedcode.github.io/joltrin/`
- Joltrin Arena (this app): `https://sharedcode.github.io/joltrin/arena/`

That workflow is the only thing in the repository that deploys to GitHub Pages; `base: './'` in `vite.config.ts` is what lets this app's built assets resolve correctly from that `/arena/` subpath.

---

## 📄 License & Attribution

Joltrin (formerly SOP / Scalable Objects Persistence) is an open-source project by [SharedCode](https://github.com/sharedcode/joltrin).
Licensed under the MIT License.
