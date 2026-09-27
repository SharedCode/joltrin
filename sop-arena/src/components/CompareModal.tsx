import React from 'react';
import { X, GitCompare, ArrowRight, ShieldAlert, ShieldCheck, Zap, Layers, Server } from 'lucide-react';

interface CompareModalProps {
  isOpen: boolean;
  onClose: () => void;
}

export const CompareModal: React.FC<CompareModalProps> = ({ isOpen, onClose }) => {
  if (!isOpen) return null;

  return (
    <div data-testid="compare-modal" role="dialog" aria-modal="true" className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-dark-950/80 backdrop-blur-md animate-fade-in">
      <div className="bg-dark-900 border border-dark-750 rounded-2xl max-w-4xl w-full max-h-[90vh] overflow-y-auto p-6 sm:p-8 shadow-2xl space-y-6">
        
        {/* Header */}
        <div className="flex items-center justify-between pb-4 border-b border-dark-800">
          <div className="flex items-center space-x-3">
            <div className="w-9 h-9 rounded-xl bg-accent-cyan/10 border border-accent-cyan/30 flex items-center justify-center text-accent-cyan">
              <GitCompare className="w-5 h-5" />
            </div>
            <div>
              <h2 className="text-xl font-bold text-white tracking-tight">Architecture Comparison</h2>
              <p className="text-xs text-slate-400">Why unifying data and compute eliminates the multi-component tax.</p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-2 rounded-lg bg-dark-850 hover:bg-dark-800 text-slate-400 hover:text-white border border-dark-700 transition"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Side-by-Side Comparison */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
          
          {/* 1. WITHOUT JOLTRIN */}
          <div className="bg-dark-950 rounded-2xl border border-rose-500/30 p-5 space-y-4">
            <div className="flex items-center justify-between">
              <span className="text-xs font-bold text-rose-400 uppercase tracking-wider font-mono flex items-center space-x-1.5">
                <ShieldAlert className="w-4 h-4" />
                <span>WITHOUT JOLTRIN (Current Stack)</span>
              </span>
              <span className="text-[10px] px-2 py-0.5 rounded bg-rose-500/10 text-rose-400 font-mono">6+ Components</span>
            </div>

            {/* Fragile Chain Diagram */}
            <div className="bg-dark-900 rounded-xl p-3 border border-dark-800 font-mono text-[11px] space-y-2 text-slate-400">
              <div className="p-2 rounded bg-dark-850 border border-dark-700 text-center text-white">Application Code</div>
              <div className="text-center text-slate-600">↓ (TCP Roundtrip #1)</div>
              <div className="p-2 rounded bg-dark-850 border border-dark-700 text-center text-amber-300">Redis (Distributed Locks)</div>
              <div className="text-center text-slate-600">↓ (TCP Roundtrip #2)</div>
              <div className="p-2 rounded bg-dark-850 border border-dark-700 text-center text-purple-300">RabbitMQ / Kafka (Task Queue)</div>
              <div className="text-center text-slate-600">↓ (TCP Roundtrip #3)</div>
              <div className="p-2 rounded bg-dark-850 border border-dark-700 text-center text-blue-300">PostgreSQL / Cassandra (DB)</div>
              <div className="text-center text-slate-600">↓ (Failover Glue)</div>
              <div className="p-2 rounded bg-dark-850 border border-rose-500/40 text-center text-rose-300">ZooKeeper / Custom Retries</div>
            </div>

            <ul className="space-y-2 text-xs text-slate-300">
              <li className="flex items-start space-x-2">
                <span className="text-rose-400 font-bold">•</span>
                <span><strong>Split-Brain Risk:</strong> If the worker crashes between Redis lock release and DB write, state enters an unrecoverable corrupted state.</span>
              </li>
              <li className="flex items-start space-x-2">
                <span className="text-rose-400 font-bold">•</span>
                <span><strong>Latency Tax:</strong> 4 separate network hops per operation (15-50ms total latency).</span>
              </li>
              <li className="flex items-start space-x-2">
                <span className="text-rose-400 font-bold">•</span>
                <span><strong>60% Glue Code:</strong> Engineering teams spend most of their time maintaining outbox patterns and lock renewers.</span>
              </li>
            </ul>
          </div>

          {/* 2. WITH JOLTRIN */}
          <div className="bg-dark-950 rounded-2xl border border-brand-500/50 p-5 space-y-4 glow-emerald">
            <div className="flex items-center justify-between">
              <span className="text-xs font-bold text-brand-400 uppercase tracking-wider font-mono flex items-center space-x-1.5">
                <ShieldCheck className="w-4 h-4" />
                <span>WITH JOLTRIN (Unified Architecture)</span>
              </span>
              <span className="text-[10px] px-2 py-0.5 rounded bg-brand-500/10 text-brand-400 font-mono font-bold">1 Single Engine</span>
            </div>

            {/* Streamlined Joltrin Diagram */}
            <div className="bg-dark-900 rounded-xl p-3 border border-dark-800 font-mono text-[11px] space-y-2 text-slate-400">
              <div className="p-2 rounded bg-dark-850 border border-dark-700 text-center text-white font-semibold">
                Application Code
              </div>
              <div className="text-center text-brand-400 font-bold">↓ (Embedded Call / Zero Network Hops)</div>
              <div className="p-3.5 rounded bg-brand-950/60 border border-brand-500/50 text-center text-brand-300 space-y-1 shadow-lg shadow-brand-500/10">
                <div className="text-xs font-bold text-white">JOLTRIN ENGINE</div>
                <div className="text-[10px] text-brand-400">Persistent B-Tree + Swarm Compute + ACID Locks + Erasure Coding</div>
              </div>
            </div>

            <ul className="space-y-2 text-xs text-slate-300">
              <li className="flex items-start space-x-2">
                <span className="text-brand-400 font-bold">•</span>
                <span><strong>Atomic Co-location:</strong> Transactions, queues, and compute live in one engine boundary. Worker crash = instant rollback & rebalance.</span>
              </li>
              <li className="flex items-start space-x-2">
                <span className="text-brand-400 font-bold">•</span>
                <span><strong>Sub-Millisecond Speed:</strong> Local in-memory execution with active NVMe/object storage persistence (&lt;0.3ms latency).</span>
              </li>
              <li className="flex items-start space-x-2">
                <span className="text-brand-400 font-bold">•</span>
                <span><strong>Zero Glue Code:</strong> Import standard Go, Python, or C# library and start executing scalable distributed tasks.</span>
              </li>
            </ul>
          </div>

        </div>

        {/* Bottom Callout */}
        <div className="bg-dark-850 rounded-xl p-4 border border-dark-750 flex flex-col sm:flex-row items-center justify-between gap-4">
          <div>
            <div className="font-bold text-white text-sm">"One engine for data and compute."</div>
            <div className="text-xs text-slate-400">Eliminate database servers, message brokers, and distributed lock appliances.</div>
          </div>
          <button
            onClick={onClose}
            className="px-5 py-2 rounded-xl bg-brand-500 hover:bg-brand-400 text-black font-semibold text-xs transition"
          >
            Back to Arena Simulation
          </button>
        </div>

      </div>
    </div>
  );
};
