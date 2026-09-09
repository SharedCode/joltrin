import React, { useState } from 'react';
import { COPILOT_KNOWLEDGE } from '../data/copilotKnowledge';
import { X, Sparkles, HelpCircle, ChevronRight, BookOpen, ShieldCheck, Cpu } from 'lucide-react';
import { CopilotQA } from '../types';

interface CopilotDrawerProps {
  isOpen: boolean;
  onClose: () => void;
}

export const CopilotDrawer: React.FC<CopilotDrawerProps> = ({ isOpen, onClose }) => {
  const [activeQA, setActiveQA] = useState<CopilotQA>(COPILOT_KNOWLEDGE[0]);

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-dark-950/60 backdrop-blur-sm animate-fade-in">
      <div className="bg-dark-900 border-l border-dark-800 w-full max-w-xl h-full flex flex-col shadow-2xl p-6 sm:p-8 overflow-y-auto space-y-6">
        
        {/* Drawer Header */}
        <div className="flex items-center justify-between pb-4 border-b border-dark-800">
          <div className="flex items-center space-x-3">
            <div className="w-9 h-9 rounded-xl bg-accent-cyan/10 border border-accent-cyan/30 flex items-center justify-center text-accent-cyan">
              <Sparkles className="w-5 h-5" />
            </div>
            <div>
              <h2 className="text-lg font-bold text-white tracking-tight">Joltrin Copilot</h2>
              <p className="text-xs text-slate-400">Interactive architectural & distributed systems explainer.</p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-2 rounded-lg bg-dark-850 hover:bg-dark-800 text-slate-400 hover:text-white border border-dark-700 transition"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Question Selector List */}
        <div className="space-y-2">
          <label className="text-[11px] font-mono uppercase tracking-wider text-slate-400 font-semibold block">
            Suggested Technical Questions:
          </label>
          <div className="space-y-1.5">
            {COPILOT_KNOWLEDGE.map((qa) => {
              const isSelected = activeQA.id === qa.id;
              return (
                <button
                  key={qa.id}
                  onClick={() => setActiveQA(qa)}
                  className={`w-full text-left p-3 rounded-xl border text-xs font-medium transition flex items-center justify-between ${
                    isSelected
                      ? 'bg-accent-cyan/10 border-accent-cyan/50 text-white font-semibold shadow-sm'
                      : 'bg-dark-950 border-dark-800 text-slate-400 hover:text-slate-200 hover:bg-dark-850'
                  }`}
                >
                  <span className="truncate pr-2">{qa.question}</span>
                  <ChevronRight className={`w-4 h-4 flex-shrink-0 ${isSelected ? 'text-accent-cyan' : 'text-slate-600'}`} />
                </button>
              );
            })}
          </div>
        </div>

        {/* Selected Answer & Deep Dive */}
        <div className="bg-dark-950 rounded-2xl border border-dark-800 p-5 space-y-4 flex-grow">
          <div className="flex items-center justify-between">
            <span className="text-[10px] px-2.5 py-0.5 rounded-full bg-brand-500/10 border border-brand-500/30 text-brand-400 font-mono font-semibold">
              {activeQA.sopFeature}
            </span>
          </div>

          <h3 className="text-base font-bold text-white">{activeQA.question}</h3>

          <div className="bg-dark-900 rounded-xl p-3.5 border border-dark-750 text-xs text-brand-300 font-medium leading-relaxed">
            {activeQA.answer}
          </div>

          <div className="space-y-2 pt-2 border-t border-dark-800">
            <h4 className="text-[11px] font-mono uppercase tracking-wider text-slate-400 font-bold flex items-center space-x-1.5">
              <BookOpen className="w-3.5 h-3.5 text-accent-cyan" />
              <span>Technical Deep Dive</span>
            </h4>
            <p className="text-xs text-slate-300 leading-relaxed">{activeQA.deepDive}</p>
          </div>
        </div>

        {/* Footer Note */}
        <div className="pt-2 text-center text-[10px] font-mono text-slate-500">
          Deterministic architectural explainer based on the open-source Joltrin engine specification.
        </div>

      </div>
    </div>
  );
};
