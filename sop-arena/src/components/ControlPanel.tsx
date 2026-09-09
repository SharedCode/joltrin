import React from 'react';
import { 
  Play, 
  Flame, 
  Bot, 
  Plus, 
  Minus, 
  Skull, 
  ShieldAlert, 
  Zap, 
  RotateCcw, 
  HeartHandshake,
  Activity
} from 'lucide-react';
import { ScenarioEvent } from '../engine/ScenarioRunner';

interface ControlPanelProps {
  onStartDisaster: () => void;
  onStartAiSwarm: () => void;
  onIncreaseLoad: (tps: number) => void;
  onAddWorker: () => void;
  onRemoveWorker: () => void;
  onKillWorker: () => void;
  onFailStorageNode: () => void;
  onCreateTxStorm: () => void;
  onSelfHealing: () => void;
  onReset: () => void;
  activeScenario: ScenarioEvent | null;
  isScenarioRunning: boolean;
  currentTps: number;
}

export const ControlPanel: React.FC<ControlPanelProps> = ({
  onStartDisaster,
  onStartAiSwarm,
  onIncreaseLoad,
  onAddWorker,
  onRemoveWorker,
  onKillWorker,
  onFailStorageNode,
  onCreateTxStorm,
  onSelfHealing,
  onReset,
  activeScenario,
  isScenarioRunning,
  currentTps,
}) => {
  return (
    <div className="bg-dark-900 border border-dark-800 rounded-2xl p-5 space-y-5">
      
      {/* Active Scenario Progress Banner (When running scripted scenario) */}
      {isScenarioRunning && activeScenario && (
        <div className="bg-gradient-to-r from-brand-950/90 via-dark-850 to-dark-900 border border-brand-500/40 rounded-xl p-4 shadow-lg shadow-brand-500/10 animate-fade-in">
          <div className="flex items-center justify-between mb-2">
            <div className="flex items-center space-x-2">
              <span className="w-2.5 h-2.5 rounded-full bg-brand-400 animate-ping"></span>
              <span className="font-mono text-xs font-bold uppercase text-brand-400">
                Step {activeScenario.stepNumber} of {activeScenario.totalSteps}: {activeScenario.title}
              </span>
            </div>
            <span className="font-mono text-xs text-brand-400 font-bold">
              {activeScenario.progressPercent}%
            </span>
          </div>
          <p className="text-xs text-slate-300 mb-2">{activeScenario.subtitle}</p>
          
          {/* Progress bar */}
          <div className="w-full bg-dark-800 h-1.5 rounded-full overflow-hidden mb-3">
            <div 
              className="bg-gradient-to-r from-brand-500 to-accent-cyan h-full transition-all duration-300"
              style={{ width: `${activeScenario.progressPercent}%` }}
            />
          </div>

          <div className="bg-dark-950/80 px-3 py-2 rounded-lg border border-dark-800 flex items-center space-x-2 text-[11px] font-mono text-slate-300">
            <span className="text-brand-400 font-bold uppercase">Joltrin Principle:</span>
            <span className="truncate">{activeScenario.sopTakeaway}</span>
          </div>
        </div>
      )}

      {/* Scripted "Aha!" Scenarios Header */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-xs font-bold text-white uppercase tracking-wider font-mono flex items-center space-x-2">
            <Flame className="w-4 h-4 text-brand-400" />
            <span>Interactive Missions</span>
          </h3>
          <span className="text-[10px] text-slate-500 font-mono">Select to start simulation</span>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          {/* Disaster Button */}
          <button
            onClick={onStartDisaster}
            disabled={isScenarioRunning}
            className={`px-4 py-3 rounded-xl font-bold text-xs flex items-center justify-center space-x-2 transition transform active:scale-95 shadow-lg ${
              isScenarioRunning
                ? 'bg-dark-800 text-slate-500 cursor-not-allowed border border-dark-700'
                : 'bg-gradient-to-r from-brand-600 via-brand-500 to-accent-cyan text-black hover:opacity-95 shadow-brand-500/25 ring-1 ring-brand-400/50'
            }`}
          >
            <Play className="w-4 h-4 fill-current" />
            <span>SURVIVE THE 60-SEC DISASTER</span>
          </button>

          {/* AI Swarm Button */}
          <button
            onClick={onStartAiSwarm}
            disabled={isScenarioRunning}
            className={`px-4 py-3 rounded-xl font-bold text-xs flex items-center justify-center space-x-2 transition transform active:scale-95 border ${
              isScenarioRunning
                ? 'bg-dark-800 text-slate-500 cursor-not-allowed border-dark-700'
                : 'bg-dark-850 hover:bg-dark-800 text-accent-cyan border-accent-cyan/40 hover:border-accent-cyan shadow-sm shadow-accent-cyan/10'
            }`}
          >
            <Bot className="w-4 h-4" />
            <span>REAL-TIME AI AGENT SWARM</span>
          </button>
        </div>
      </div>

      {/* Manual Sandbox Controls */}
      <div className="border-t border-dark-800 pt-4 space-y-3">
        <div className="flex items-center justify-between">
          <h4 className="text-[11px] font-semibold uppercase tracking-wider text-slate-400 font-mono">
            Live Chaos & Swarm Injections
          </h4>
          <span className="text-[10px] text-slate-500 font-mono">Test fault tolerance</span>
        </div>

        {/* Workload Ingestion Slider/Buttons */}
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs font-mono text-slate-400 flex items-center space-x-1">
            <Activity className="w-3.5 h-3.5 text-blue-400" />
            <span>Workload:</span>
          </span>
          <button 
            onClick={() => onIncreaseLoad(10000)}
            className={`px-2.5 py-1 rounded-md text-xs font-mono transition border ${
              currentTps <= 15000 ? 'bg-blue-500/20 border-blue-500/40 text-blue-300' : 'bg-dark-950 border-dark-800 text-slate-400 hover:text-white'
            }`}
          >
            10k TPS
          </button>
          <button 
            onClick={() => onIncreaseLoad(35000)}
            className={`px-2.5 py-1 rounded-md text-xs font-mono transition border ${
              currentTps > 15000 && currentTps <= 50000 ? 'bg-blue-500/20 border-blue-500/40 text-blue-300' : 'bg-dark-950 border-dark-800 text-slate-400 hover:text-white'
            }`}
          >
            35k TPS
          </button>
          <button 
            onClick={() => onIncreaseLoad(75000)}
            className={`px-2.5 py-1 rounded-md text-xs font-mono transition border ${
              currentTps > 50000 ? 'bg-amber-500/20 border-amber-500/40 text-amber-300' : 'bg-dark-950 border-dark-800 text-slate-400 hover:text-white'
            }`}
          >
            75k TPS (Spike)
          </button>
        </div>

        {/* Chaos Action Buttons Grid */}
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-2 pt-1">
          
          <button
            onClick={onAddWorker}
            className="px-2.5 py-2 rounded-lg bg-dark-950 hover:bg-dark-800 border border-dark-700 text-xs font-medium text-slate-200 flex items-center justify-center space-x-1.5 transition"
            title="Scale compute swarm"
          >
            <Plus className="w-3.5 h-3.5 text-brand-400" />
            <span>Add Worker</span>
          </button>

          <button
            onClick={onRemoveWorker}
            className="px-2.5 py-2 rounded-lg bg-dark-950 hover:bg-dark-800 border border-dark-700 text-xs font-medium text-slate-200 flex items-center justify-center space-x-1.5 transition"
            title="Remove compute worker"
          >
            <Minus className="w-3.5 h-3.5 text-slate-400" />
            <span>Remove Worker</span>
          </button>

          <button
            onClick={onKillWorker}
            className="px-2.5 py-2 rounded-lg bg-dark-950 hover:bg-rose-950/40 border border-rose-500/40 text-xs font-medium text-rose-300 flex items-center justify-center space-x-1.5 transition"
            title="Simulate compute agent crash"
          >
            <Skull className="w-3.5 h-3.5 text-rose-400" />
            <span>Kill Worker</span>
          </button>

          <button
            onClick={onFailStorageNode}
            className="px-2.5 py-2 rounded-lg bg-dark-950 hover:bg-rose-950/40 border border-rose-500/40 text-xs font-medium text-rose-300 flex items-center justify-center space-x-1.5 transition"
            title="Simulate storage disk failure"
          >
            <ShieldAlert className="w-3.5 h-3.5 text-rose-400" />
            <span>Fail Node</span>
          </button>

          <button
            onClick={onCreateTxStorm}
            className="px-2.5 py-2 rounded-lg bg-dark-950 hover:bg-amber-950/40 border border-amber-500/40 text-xs font-medium text-amber-300 flex items-center justify-center space-x-1.5 transition"
            title="Simulate concurrent transaction contention"
          >
            <Zap className="w-3.5 h-3.5 text-amber-400" />
            <span>Tx Storm</span>
          </button>

          <button
            onClick={onSelfHealing}
            className="px-2.5 py-2 rounded-lg bg-dark-950 hover:bg-brand-950/40 border border-brand-500/40 text-xs font-medium text-brand-300 flex items-center justify-center space-x-1.5 transition"
            title="Force immediate self-healing"
          >
            <HeartHandshake className="w-3.5 h-3.5 text-brand-400" />
            <span>Self-Heal</span>
          </button>

        </div>

        <div className="flex justify-end pt-1">
          <button
            onClick={onReset}
            className="text-[11px] font-mono text-slate-500 hover:text-slate-300 flex items-center space-x-1 transition"
          >
            <RotateCcw className="w-3 h-3" />
            <span>Reset System Baseline</span>
          </button>
        </div>
      </div>

    </div>
  );
};
