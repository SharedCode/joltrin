import React, { useState, useEffect, useRef } from 'react';
import { 
  TopologyNode, 
  JobParticle, 
  SystemMetrics, 
  LogEntry, 
  DataRecord, 
  ViewMode 
} from './types';
import { SimulationBackend } from './engine/SimulationBackend';
import { ScenarioRunner, ScenarioEvent } from './engine/ScenarioRunner';
import { Header } from './components/Header';
import { TopologyGraph } from './components/TopologyGraph';
import { MetricsGrid } from './components/MetricsGrid';
import { ControlPanel } from './components/ControlPanel';
import { EventLogStream } from './components/EventLogStream';
import { CompareModal } from './components/CompareModal';
import { InvestorModeView } from './components/InvestorModeView';
import { CopilotDrawer } from './components/CopilotDrawer';
import { MissionSuccessModal } from './components/MissionSuccessModal';
import { DataInspectorModal } from './components/DataInspectorModal';
import { EnterpriseInterestModal } from './components/EnterpriseInterestModal';
import { 
  Database, 
  GitCompare, 
  Sparkles, 
  Layers, 
  Flame, 
  Play, 
  Briefcase,
  ShieldCheck,
  Bot,
  Building2,
  Cpu,
  Zap,
  Lock,
  Check,
  ArrowRight,
  ExternalLink
} from 'lucide-react';

export const App: React.FC = () => {
  const backendRef = useRef<SimulationBackend | null>(null);
  const scenarioRef = useRef<ScenarioRunner | null>(null);

  // Application State
  const [viewMode, setViewMode] = useState<ViewMode>('arena');
  const [nodes, setNodes] = useState<TopologyNode[]>([]);
  const [particles, setParticles] = useState<JobParticle[]>([]);
  const [isEnterpriseOpen, setIsEnterpriseOpen] = useState(false);
  const [selectedTier, setSelectedTier] = useState<'pro' | 'enterprise' | 'hosted' | 'not_sure'>('enterprise');
  const [metrics, setMetrics] = useState<SystemMetrics>({
    tps: 15000,
    totalTransactions: 150000,
    consistencyRate: 100.00,
    activeWorkers: 6,
    totalWorkers: 6,
    activeStorageNodes: 4,
    totalStorageNodes: 4,
    avgLatencyMs: 0.28,
    p99LatencyMs: 0.84,
    reliabilityScore: 99.2,
    redistributedJobs: 0,
    erasureChunksRecovered: 0,
    conflictsResolved: 0,
    glueComponentsEliminated: 6,
  });
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [records, setRecords] = useState<DataRecord[]>([]);

  // Modals & Drawers
  const [isCopilotOpen, setIsCopilotOpen] = useState(false);
  const [isCompareOpen, setIsCompareOpen] = useState(false);
  const [isSuccessOpen, setIsSuccessOpen] = useState(false);
  const [isDataInspectorOpen, setIsDataInspectorOpen] = useState(false);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);

  // Scenario Progress
  const [activeScenario, setActiveScenario] = useState<ScenarioEvent | null>(null);
  const [isScenarioRunning, setIsScenarioRunning] = useState(false);

  // Initialize Backend & Listeners
  useEffect(() => {
    const backend = new SimulationBackend();
    backendRef.current = backend;
    scenarioRef.current = new ScenarioRunner(backend);

    backend.init({
      onNodesUpdate: (updatedNodes) => setNodes(updatedNodes),
      onMetricsUpdate: (updatedMetrics) => setMetrics(updatedMetrics),
      onParticlesUpdate: (updatedParticles) => setParticles(updatedParticles),
      onRecordsUpdate: (updatedRecords) => setRecords(updatedRecords),
      onLogEntry: (newLog) => {
        setLogs((prev) => {
          const next = [...prev, newLog];
          if (next.length > 250) return next.slice(next.length - 250);
          return next;
        });
      },
    });

    return () => {
      backend.destroy();
      if (scenarioRef.current) {
        scenarioRef.current.stop();
      }
    };
  }, []);

  // Scenario Handlers
  const handleStartDisaster = () => {
    if (!scenarioRef.current) return;
    setIsScenarioRunning(true);
    scenarioRef.current.runDisaster60s((event, isFinished) => {
      setActiveScenario(event);
      if (isFinished) {
        setIsScenarioRunning(false);
        setActiveScenario(null);
        setIsSuccessOpen(true);
      }
    });
  };

  const handleStartAiSwarm = () => {
    if (!scenarioRef.current) return;
    setIsScenarioRunning(true);
    scenarioRef.current.runAiAgentWorkforce((event, isFinished) => {
      setActiveScenario(event);
      if (isFinished) {
        setIsScenarioRunning(false);
        setActiveScenario(null);
        setIsSuccessOpen(true);
      }
    });
  };

  return (
    <div className="min-h-screen flex flex-col bg-dark-950 text-slate-100">
      
      {/* Top Navbar */}
      <Header
        currentMode={viewMode}
        onSelectMode={(mode) => setViewMode(mode)}
        onOpenCopilot={() => setIsCopilotOpen(true)}
        onOpenCompare={() => setIsCompareOpen(true)}
        onOpenEnterprise={(tier) => {
          setSelectedTier(tier || 'enterprise');
          setIsEnterpriseOpen(true);
        }}
        reliabilityScore={metrics.reliabilityScore}
      />

      {/* Main Content Area */}
      <main className="flex-grow max-w-7xl w-full mx-auto px-4 sm:px-6 lg:px-8 py-6 space-y-6">
        
        {viewMode === 'investor' ? (
          <InvestorModeView onBackToArena={() => setViewMode('arena')} />
        ) : (
          <div className="space-y-6">
            
            {/* Arena Hero Banner */}
            <section className="bg-gradient-to-r from-dark-900 via-dark-850 to-dark-900 border border-dark-800 rounded-3xl p-6 sm:p-8 relative overflow-hidden shadow-xl">
              <div className="relative z-10 flex flex-col lg:flex-row items-start lg:items-center justify-between gap-6">
                <div className="max-w-2xl space-y-2">
                  <div className="inline-flex items-center space-x-2 px-2.5 py-0.5 rounded-full bg-brand-500/10 border border-brand-500/30 text-brand-400 text-xs font-mono font-semibold">
                    <Flame className="w-3.5 h-3.5" />
                    <span>JOLTRIN ARENA: Keep the System Alive</span>
                  </div>
                  <h1 className="text-2xl sm:text-3xl lg:text-4xl font-extrabold text-white tracking-tight">
                    Can Your Infrastructure Survive 100k TPS & Node Crashes?
                  </h1>
                  <p className="text-slate-300 text-sm leading-relaxed">
                    Experience what happens when persistence, transactions, and swarm compute live in <strong>one unified engine</strong>. Break nodes, spawn transaction storms, and watch Joltrin recover automatically with zero glue code.
                  </p>
                </div>

                <div className="flex flex-wrap items-center gap-3">
                  <button
                    onClick={handleStartDisaster}
                    disabled={isScenarioRunning}
                    className="px-5 py-3 rounded-xl bg-gradient-to-r from-brand-600 to-brand-500 hover:from-brand-500 hover:to-brand-400 text-black font-bold text-xs shadow-lg shadow-brand-500/25 transition transform active:scale-95 flex items-center space-x-2"
                  >
                    <Play className="w-4 h-4 fill-current" />
                    <span>SURVIVE THE 60-SEC DISASTER</span>
                  </button>

                  <button
                    onClick={() => setViewMode('investor')}
                    className="px-4 py-3 rounded-xl bg-dark-800 hover:bg-dark-750 border border-dark-700 text-slate-200 font-semibold text-xs flex items-center space-x-2 transition"
                  >
                    <Briefcase className="w-4 h-4 text-accent-cyan" />
                    <span>Investor Deck</span>
                  </button>
                </div>
              </div>
            </section>

            {/* Live Metrics Grid */}
            <MetricsGrid metrics={metrics} />

            {/* Live Distributed Topology Canvas */}
            <div className="space-y-2">
              <div className="flex items-center justify-between px-1">
                <div className="flex items-center space-x-2 text-xs font-bold text-white uppercase tracking-wider font-mono">
                  <Layers className="w-4 h-4 text-brand-400" />
                  <span>Live Distributed Systems Topology</span>
                </div>
                <div className="flex items-center space-x-2">
                  <button
                    onClick={() => setIsDataInspectorOpen(true)}
                    className="text-xs px-3 py-1 rounded-lg bg-dark-900 hover:bg-dark-850 text-accent-violet border border-accent-violet/30 font-mono flex items-center space-x-1.5 transition"
                  >
                    <Database className="w-3.5 h-3.5" />
                    <span>Inspect B-Tree Data</span>
                  </button>
                  <button
                    onClick={() => setIsCompareOpen(true)}
                    className="text-xs px-3 py-1 rounded-lg bg-dark-900 hover:bg-dark-850 text-brand-400 border border-brand-500/30 font-mono flex items-center space-x-1.5 transition"
                  >
                    <GitCompare className="w-3.5 h-3.5" />
                    <span>Without Joltrin vs With Joltrin</span>
                  </button>
                </div>
              </div>

              <TopologyGraph
                nodes={nodes}
                particles={particles}
                selectedNodeId={selectedNodeId}
                onSelectNode={(node) => setSelectedNodeId(node.id)}
              />
            </div>

            {/* Control Panel & Real-time Event Stream (2 Columns) */}
            <div className="grid grid-cols-1 lg:grid-cols-12 gap-6">
              <div className="lg:col-span-6">
                <ControlPanel
                  onStartDisaster={handleStartDisaster}
                  onStartAiSwarm={handleStartAiSwarm}
                  onIncreaseLoad={(tps) => backendRef.current?.setTargetTps(tps)}
                  onAddWorker={() => backendRef.current?.addWorker()}
                  onRemoveWorker={() => backendRef.current?.removeWorker()}
                  onKillWorker={() => backendRef.current?.killWorker()}
                  onFailStorageNode={() => backendRef.current?.failStorageNode()}
                  onCreateTxStorm={() => backendRef.current?.createTransactionStorm()}
                  onSelfHealing={() => backendRef.current?.triggerSelfHealing()}
                  onReset={() => backendRef.current?.resetSystem()}
                  activeScenario={activeScenario}
                  isScenarioRunning={isScenarioRunning}
                  currentTps={metrics.tps}
                />
              </div>

              <div className="lg:col-span-6">
                <EventLogStream
                  logs={logs}
                  onClearLogs={() => setLogs([])}
                />
              </div>
            </div>

            {/* Post-Demo Section 1: What You're Seeing in Action */}
            <section className="bg-dark-900 border border-dark-800 rounded-3xl p-6 sm:p-8 space-y-6">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 pb-4 border-b border-dark-800">
                <div>
                  <div className="inline-flex items-center space-x-2 px-2.5 py-0.5 rounded-full bg-brand-500/10 border border-brand-500/30 text-brand-400 text-xs font-mono font-semibold uppercase tracking-wider mb-2">
                    <ShieldCheck className="w-3.5 h-3.5" />
                    <span>Technical Architecture Demystified</span>
                  </div>
                  <h2 className="text-xl sm:text-2xl font-extrabold text-white tracking-tight">
                    What You're Seeing in Action
                  </h2>
                  <p className="text-slate-400 text-xs sm:text-sm mt-1">
                    How Joltrin's distributed Go kernel coordinates transactions and compute under catastrophic failure.
                  </p>
                </div>
                <div className="flex items-center space-x-2 font-mono text-xs text-slate-400">
                  <span className="w-2 h-2 rounded-full bg-brand-400 animate-pulse"></span>
                  <span>Zero-Loss Architecture</span>
                </div>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                <div className="bg-dark-950 p-5 rounded-2xl border border-dark-800 space-y-3">
                  <div className="w-8 h-8 rounded-xl bg-brand-500/10 border border-brand-500/30 flex items-center justify-center text-brand-400">
                    <Cpu className="w-4 h-4" />
                  </div>
                  <h3 className="text-sm font-bold text-white">Autonomous Worker Re-balancing</h3>
                  <p className="text-xs text-slate-400 leading-relaxed">
                    When you kill a worker node during peak load, Joltrin's distributed lease manager detects heartbeats within milliseconds. In-flight jobs are safely aborted, rolled back, and redistributed across surviving workers without losing transactional state.
                  </p>
                </div>

                <div className="bg-dark-950 p-5 rounded-2xl border border-dark-800 space-y-3">
                  <div className="w-8 h-8 rounded-xl bg-accent-cyan/10 border border-accent-cyan/30 flex items-center justify-center text-accent-cyan">
                    <Database className="w-4 h-4" />
                  </div>
                  <h3 className="text-sm font-bold text-white">Erasure Coding &amp; Storage Resiliency</h3>
                  <p className="text-xs text-slate-400 leading-relaxed">
                    Storage nodes use Reed-Solomon erasure coding chunks. If a storage node goes offline, missing data fragments are instantly reconstructed in-flight from parity blocks with zero read disruptions or corrupted commits.
                  </p>
                </div>

                <div className="bg-dark-950 p-5 rounded-2xl border border-dark-800 space-y-3">
                  <div className="w-8 h-8 rounded-xl bg-accent-violet/10 border border-accent-violet/30 flex items-center justify-center text-accent-violet">
                    <Zap className="w-4 h-4" />
                  </div>
                  <h3 className="text-sm font-bold text-white">Eliminating the Multi-Tier Tax</h3>
                  <p className="text-xs text-slate-400 leading-relaxed">
                    Traditional stacks bolt Redis to Kafka, Kafka to Postgres, and Postgres to ZooKeeper. Joltrin combines durable B-Tree indexing, ACID transactions, and compute streaming into one unified engine, cutting 6 failure points.
                  </p>
                </div>
              </div>
            </section>

            {/* Post-Demo Section 2: Take This Into Production */}
            <section className="bg-gradient-to-br from-dark-900 via-dark-850 to-dark-900 border border-brand-500/30 rounded-3xl p-6 sm:p-10 space-y-8 relative overflow-hidden shadow-2xl">
              <div className="relative z-10 max-w-3xl space-y-3">
                <div className="inline-flex items-center space-x-2 px-3 py-1 rounded-full bg-brand-500/10 border border-brand-500/30 text-brand-400 text-xs font-mono font-semibold uppercase tracking-wider">
                  <Building2 className="w-3.5 h-3.5" />
                  <span>Commercial &amp; Enterprise Infrastructure</span>
                </div>
                <h2 className="text-2xl sm:text-3xl font-extrabold text-white tracking-tight">
                  Take This Resilience Into Your Production AI Agent Fleets
                </h2>
                <p className="text-slate-300 text-sm leading-relaxed">
                  The same engine running this simulation runs in production as a high-performance Go daemon or embedded WebAssembly library. Give your autonomous agents durable memory, instant crash recovery, and security barriers before they act.
                </p>
              </div>

              {/* 3 Commercial Tiers Grid */}
              <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 relative z-10">
                {/* Tier 1: Free Open Source */}
                <div className="bg-dark-950/80 rounded-2xl border border-dark-800 p-6 flex flex-col justify-between space-y-4">
                  <div className="space-y-3">
                    <div className="flex items-center justify-between">
                      <span className="text-xs font-bold font-mono uppercase text-slate-400">Open Core</span>
                      <span className="text-[10px] px-2 py-0.5 rounded bg-dark-800 text-slate-300 font-mono">MIT License</span>
                    </div>
                    <div className="text-2xl font-black text-white">$0</div>
                    <p className="text-xs text-slate-400">Full embedded engine for local agents and single-node persistence.</p>
                    <ul className="text-xs space-y-2 text-slate-300 pt-2 border-t border-dark-850 font-mono">
                      <li className="flex items-start space-x-2"><Check className="w-3.5 h-3.5 text-brand-400 flex-shrink-0 mt-0.5" /><span>Go library &amp; WASM runtime</span></li>
                      <li className="flex items-start space-x-2"><Check className="w-3.5 h-3.5 text-brand-400 flex-shrink-0 mt-0.5" /><span>Full ACID transaction guarantees</span></li>
                      <li className="flex items-start space-x-2"><Check className="w-3.5 h-3.5 text-brand-400 flex-shrink-0 mt-0.5" /><span>Vector search &amp; B-Tree indexes</span></li>
                    </ul>
                  </div>
                  <a
                    href="https://github.com/SharedCode/joltrin"
                    target="_blank"
                    rel="noopener noreferrer"
                    className="w-full py-2.5 px-4 rounded-xl bg-dark-800 hover:bg-dark-750 text-white font-semibold text-xs text-center border border-dark-700 transition"
                  >
                    View on GitHub
                  </a>
                </div>

                {/* Tier 2: Pro */}
                <div className="bg-dark-950/90 rounded-2xl border border-brand-500/40 p-6 flex flex-col justify-between space-y-4 shadow-xl shadow-brand-500/5 relative">
                  <div className="absolute -top-3 right-4 px-2 py-0.5 bg-brand-500 text-black text-[10px] font-bold font-mono rounded-full uppercase">
                    Most Popular
                  </div>
                  <div className="space-y-3">
                    <div className="flex items-center justify-between">
                      <span className="text-xs font-bold font-mono uppercase text-brand-400">Pro Edition</span>
                      <span className="text-[10px] px-2 py-0.5 rounded bg-brand-500/10 text-brand-400 font-mono">Teams</span>
                    </div>
                    <div className="text-2xl font-black text-white">$49 <span className="text-xs font-normal text-slate-400">/ workspace / mo</span></div>
                    <p className="text-xs text-slate-400">Multi-agent governance, encrypted replication, and team access controls.</p>
                    <ul className="text-xs space-y-2 text-slate-300 pt-2 border-t border-dark-850 font-mono">
                      <li className="flex items-start space-x-2"><Check className="w-3.5 h-3.5 text-brand-400 flex-shrink-0 mt-0.5" /><span>Multi-workspace agent policies</span></li>
                      <li className="flex items-start space-x-2"><Check className="w-3.5 h-3.5 text-brand-400 flex-shrink-0 mt-0.5" /><span>Automated off-site snapshot replication</span></li>
                      <li className="flex items-start space-x-2"><Check className="w-3.5 h-3.5 text-brand-400 flex-shrink-0 mt-0.5" /><span>Stripe-backed billing &amp; priority SLAs</span></li>
                    </ul>
                  </div>
                  <button
                    onClick={() => {
                      setSelectedTier('pro');
                      setIsEnterpriseOpen(true);
                    }}
                    className="w-full py-2.5 px-4 rounded-xl bg-brand-500 hover:bg-brand-400 text-black font-bold text-xs transition shadow-md shadow-brand-500/20"
                  >
                    Get Started with Pro
                  </button>
                </div>

                {/* Tier 3: Enterprise */}
                <div className="bg-dark-950/80 rounded-2xl border border-accent-cyan/30 p-6 flex flex-col justify-between space-y-4">
                  <div className="space-y-3">
                    <div className="flex items-center justify-between">
                      <span className="text-xs font-bold font-mono uppercase text-accent-cyan">Enterprise</span>
                      <span className="text-[10px] px-2 py-0.5 rounded bg-accent-cyan/10 text-accent-cyan font-mono">Custom</span>
                    </div>
                    <div className="text-2xl font-black text-white">Custom SLA</div>
                    <p className="text-xs text-slate-400">Strict compliance, Okta / Entra ID SSO, dedicated architecture support.</p>
                    <ul className="text-xs space-y-2 text-slate-300 pt-2 border-t border-dark-850 font-mono">
                      <li className="flex items-start space-x-2"><Check className="w-3.5 h-3.5 text-brand-400 flex-shrink-0 mt-0.5" /><span>Okta &amp; Microsoft Entra ID SSO integration</span></li>
                      <li className="flex items-start space-x-2"><Check className="w-3.5 h-3.5 text-brand-400 flex-shrink-0 mt-0.5" /><span>Tamper-evident audit trails &amp; RBAC</span></li>
                      <li className="flex items-start space-x-2"><Check className="w-3.5 h-3.5 text-brand-400 flex-shrink-0 mt-0.5" /><span>Custom HA topologies &amp; 99.99% uptime SLA</span></li>
                    </ul>
                  </div>
                  <button
                    onClick={() => {
                      setSelectedTier('enterprise');
                      setIsEnterpriseOpen(true);
                    }}
                    className="w-full py-2.5 px-4 rounded-xl bg-dark-800 hover:bg-dark-750 text-accent-cyan font-semibold text-xs border border-accent-cyan/40 transition"
                  >
                    Contact Enterprise Sales
                  </button>
                </div>
              </div>

              {/* Cloud Waitlist Banner */}
              <div className="bg-dark-950/60 border border-dark-800 rounded-2xl p-4 flex flex-col sm:flex-row items-center justify-between gap-4 text-xs">
                <div className="flex items-center space-x-3">
                  <div className="p-2 rounded-lg bg-dark-850 border border-dark-700 text-slate-400">
                    <Lock className="w-4 h-4" />
                  </div>
                  <div>
                    <span className="font-semibold text-white">Looking for Hosted Joltrin Cloud?</span>
                    <span className="text-[10px] ml-2 px-2 py-0.5 rounded bg-amber-500/10 text-amber-400 border border-amber-500/20 font-mono">Planned / Coming Soon</span>
                    <p className="text-slate-400 text-[11px] mt-0.5">We are onboarding design partners for managed serverless agent clusters.</p>
                  </div>
                </div>
                <button
                  onClick={() => {
                    setSelectedTier('hosted');
                    setIsEnterpriseOpen(true);
                  }}
                  className="px-4 py-2 rounded-xl bg-dark-850 hover:bg-dark-800 border border-dark-700 text-slate-300 font-mono text-xs whitespace-nowrap transition"
                >
                  Join Cloud Waitlist →
                </button>
              </div>
            </section>

          </div>
        )}

      </main>

      {/* Cohesive Commercial Footer */}
      <footer className="border-t border-dark-800 bg-dark-950 py-10 text-xs text-slate-400">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 grid grid-cols-1 md:grid-cols-4 gap-8 mb-6">
          <div className="space-y-2 md:col-span-2">
            <div className="flex items-center space-x-2">
              <span className="font-extrabold text-white text-base">Joltrin Arena</span>
              <span className="text-[10px] px-2 py-0.5 rounded bg-brand-500/10 text-brand-400 border border-brand-500/30 font-mono">Interactive Simulation</span>
            </div>
            <p className="text-xs text-slate-400 max-w-md leading-relaxed">
              Stress-test distributed transactions, swarm compute, and erasure-coded storage in real time with zero external dependencies.
            </p>
            <p className="text-[11px] text-slate-500 font-mono">
              Canonical custom domain: <a href="https://joltrin.com/arena/" className="text-brand-400 hover:underline">joltrin.com/arena</a> · Open source core under MIT
            </p>
          </div>
          <div>
            <h4 className="font-mono text-xs uppercase font-bold text-white mb-2">Product Funnel</h4>
            <ul className="space-y-1.5 text-xs font-mono">
              <li><a href="../" className="hover:text-white transition">🧠 Technical Demo &amp; Engine</a></li>
              <li><a href="./" className="hover:text-white transition text-brand-400">🎮 Joltrin Arena (Simulation)</a></li>
              <li><a href="../agents/" className="hover:text-white transition">🔌 Joltrin Agents (Safety Barrier)</a></li>
              <li><button onClick={() => { setSelectedTier('enterprise'); setIsEnterpriseOpen(true); }} className="hover:text-white transition text-left">🏢 Enterprise &amp; Pricing</button></li>
            </ul>
          </div>
          <div>
            <h4 className="font-mono text-xs uppercase font-bold text-white mb-2">Open Source</h4>
            <ul className="space-y-1.5 text-xs font-mono">
              <li><a href="https://github.com/SharedCode/joltrin" target="_blank" rel="noopener noreferrer" className="hover:text-white transition">GitHub Repository</a></li>
              <li><a href="https://github.com/SharedCode/joltrin#readme" target="_blank" rel="noopener noreferrer" className="hover:text-white transition">Documentation</a></li>
              <li><a href="https://github.com/SharedCode/joltrin/blob/master/LICENSE" target="_blank" rel="noopener noreferrer" className="hover:text-white transition">MIT License</a></li>
            </ul>
          </div>
        </div>
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 pt-4 border-t border-dark-850 flex flex-col sm:flex-row items-center justify-between gap-2 text-[11px] text-slate-500 font-mono">
          <div>© 2026 Joltrin Authors &amp; SharedCode. One engine for data and compute.</div>
          <div>Sub-millisecond ACID Latency • Zero Glue Overhead</div>
        </div>
      </footer>

      {/* Modals & Drawers */}
      <EnterpriseInterestModal
        isOpen={isEnterpriseOpen}
        onClose={() => setIsEnterpriseOpen(false)}
        initialTier={selectedTier}
      />

      <CompareModal
        isOpen={isCompareOpen}
        onClose={() => setIsCompareOpen(false)}
      />

      <CopilotDrawer
        isOpen={isCopilotOpen}
        onClose={() => setIsCopilotOpen(false)}
      />

      <MissionSuccessModal
        isOpen={isSuccessOpen}
        onClose={() => setIsSuccessOpen(false)}
        metrics={metrics}
        onTryAgain={handleStartDisaster}
      />

      <DataInspectorModal
        isOpen={isDataInspectorOpen}
        onClose={() => setIsDataInspectorOpen(false)}
        records={records}
      />

    </div>
  );
};

export default App;
