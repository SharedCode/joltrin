import React from 'react';
import { 
  Trophy, 
  Share2, 
  RotateCcw, 
} from 'lucide-react';
import { GithubIcon } from './GithubIcon';
import { SystemMetrics } from '../types';

interface MissionSuccessModalProps {
  isOpen: boolean;
  onClose: () => void;
  metrics: SystemMetrics;
  onTryAgain: () => void;
}

export const MissionSuccessModal: React.FC<MissionSuccessModalProps> = ({
  isOpen,
  onClose,
  metrics,
  onTryAgain,
}) => {
  if (!isOpen) return null;

  const handleCopyShare = () => {
    const text = `I just survived the Joltrin Distributed Systems Disaster with a ${metrics.reliabilityScore.toFixed(1)}% reliability score and 0 dropped writes! Try it: https://joltrinhq.com/arena/`;
    navigator.clipboard.writeText(text).then(() => {
      alert('Challenge link copied to clipboard!');
    });
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-dark-950/85 backdrop-blur-md animate-fade-in">
      <div className="bg-dark-900 border border-brand-500/40 rounded-3xl max-w-2xl w-full p-6 sm:p-10 shadow-2xl space-y-6 text-center glow-emerald">
        
        {/* Trophy Badge */}
        <div className="w-16 h-16 rounded-2xl bg-gradient-to-tr from-brand-600 to-accent-cyan mx-auto flex items-center justify-center text-black shadow-xl shadow-brand-500/25">
          <Trophy className="w-8 h-8" />
        </div>

        <div className="space-y-2">
          <span className="text-xs font-mono font-bold uppercase tracking-wider text-brand-400 px-3 py-1 rounded-full bg-brand-500/10 border border-brand-500/30">
            Mission Accomplished
          </span>
          <h2 className="text-3xl sm:text-4xl font-extrabold text-white tracking-tight">
            SYSTEM SURVIVED.
          </h2>
          <p className="text-sm sm:text-base text-slate-300 max-w-lg mx-auto leading-relaxed">
            You just experienced what happens when storage, transactions, compute, and coordination live in <strong className="text-white">one unified engine</strong>.
          </p>
        </div>

        {/* The Equation Card */}
        <div className="bg-dark-950 p-4 rounded-2xl border border-dark-800 font-mono text-xs text-slate-300 space-y-2">
          <div className="flex flex-wrap items-center justify-center gap-2 text-slate-400">
            <span>DATA</span>
            <span className="text-brand-400 font-bold">+</span>
            <span>COMPUTE</span>
            <span className="text-brand-400 font-bold">+</span>
            <span>COORDINATION</span>
            <span className="text-brand-400 font-bold">+</span>
            <span>ACID TRANSACTIONS</span>
          </div>
          <div className="text-brand-400 font-bold text-sm">
            ↓ <br />
            Joltrin (formerly SOP)
          </div>
        </div>

        {/* Mission Stats Breakdown */}
        <div className="grid grid-cols-3 gap-3">
          <div className="bg-dark-950 p-3 rounded-xl border border-dark-800">
            <span className="text-[10px] text-slate-500 font-mono block">Reliability Score</span>
            <span className="text-xl font-bold font-mono text-brand-400">{metrics.reliabilityScore.toFixed(1)}%</span>
          </div>
          <div className="bg-dark-950 p-3 rounded-xl border border-dark-800">
            <span className="text-[10px] text-slate-500 font-mono block">ACID Consistency</span>
            <span className="text-xl font-bold font-mono text-accent-cyan">{metrics.consistencyRate.toFixed(2)}%</span>
          </div>
          <div className="bg-dark-950 p-3 rounded-xl border border-dark-800">
            <span className="text-[10px] text-slate-500 font-mono block">Dropped Writes</span>
            <span className="text-xl font-bold font-mono text-white">0</span>
          </div>
        </div>

        {/* Call to Actions */}
        <div className="flex flex-col sm:flex-row gap-3 pt-2">
          <a
            href="https://github.com/sharedcode/joltrin"
            target="_blank"
            rel="noopener noreferrer"
            className="flex-1 py-3 px-4 rounded-xl bg-gradient-to-r from-brand-600 to-brand-500 hover:from-brand-500 hover:to-brand-400 font-bold text-xs text-black shadow-lg shadow-brand-500/20 flex items-center justify-center space-x-2 transition"
          >
            <GithubIcon className="w-4 h-4 text-black" />
            <span>EXPLORE JOLTRIN ON GITHUB</span>
          </a>

          <button
            onClick={handleCopyShare}
            className="py-3 px-4 rounded-xl bg-dark-850 hover:bg-dark-800 text-slate-200 border border-dark-750 font-semibold text-xs flex items-center justify-center space-x-2 transition"
          >
            <Share2 className="w-4 h-4" />
            <span>Challenge a Teammate</span>
          </button>

          <button
            onClick={() => {
              onClose();
              onTryAgain();
            }}
            className="py-3 px-4 rounded-xl bg-dark-850 hover:bg-dark-800 text-slate-400 hover:text-white border border-dark-750 font-semibold text-xs flex items-center justify-center space-x-1.5 transition"
          >
            <RotateCcw className="w-4 h-4" />
            <span>Try Again</span>
          </button>
        </div>

      </div>
    </div>
  );
};
