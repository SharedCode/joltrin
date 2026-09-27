import React, { useState } from 'react';
import { ViewMode } from '../types';
import {
  Volume2,
  VolumeX,
  Share2,
  Check,
  Sparkles,
  Flame,
  Briefcase,
  GitCompare,
  Building2,
  Menu,
  X,
} from 'lucide-react';
import { GithubIcon } from './GithubIcon';
import { sounds } from '../engine/SoundEffects';

interface HeaderProps {
  currentMode: ViewMode;
  onSelectMode: (mode: ViewMode) => void;
  onOpenCopilot: () => void;
  onOpenCompare: () => void;
  onOpenEnterprise: (tier?: 'pro' | 'enterprise') => void;
  reliabilityScore: number;
}

export const Header: React.FC<HeaderProps> = ({
  currentMode,
  onSelectMode,
  onOpenCopilot,
  onOpenCompare,
  onOpenEnterprise,
  reliabilityScore,
}) => {
  const [isMuted, setIsMuted] = useState(!sounds.isEnabled());
  const [copied, setCopied] = useState(false);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);

  const toggleSound = () => {
    const enabled = sounds.toggle();
    setIsMuted(!enabled);
  };

  const handleShare = () => {
    const url = window.location.href;
    navigator.clipboard.writeText(url).then(() => {
      setCopied(true);
      sounds.playCommit();
      setTimeout(() => setCopied(false), 2000);
    });
  };

  return (
    <header className="border-b border-dark-800 bg-dark-950/80 backdrop-blur-md sticky top-0 z-40">
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between gap-4">
        
        {/* Brand Logo & Title */}
        <div className="flex items-center space-x-3 flex-shrink-0">
          <img src="./logo-mark.png" alt="Joltrin" className="w-9 h-9 object-contain flex-shrink-0" width={36} height={36} />
          <div className="flex-shrink-0">
            <div className="flex items-center space-x-2">
              <span className="font-extrabold tracking-tight text-white text-base sm:text-lg whitespace-nowrap leading-none">JOLTRIN ARENA</span>
              <span className="text-[10px] px-2 py-0.5 rounded-full bg-brand-500/10 border border-brand-500/30 text-brand-400 font-mono font-semibold uppercase whitespace-nowrap hidden sm:inline-block">
                Interactive Sim
              </span>
            </div>
            <p className="text-[10px] text-slate-400 font-mono hidden md:block whitespace-nowrap mt-0.5">
              Distributed systems survival simulation
            </p>
          </div>
        </div>

        {/* View Mode Navigation Switcher & Cross-Demo Links */}
        <div className="hidden md:flex items-center space-x-1 bg-dark-900 p-1 rounded-xl border border-dark-800">
          <a
            href="../"
            className="px-2.5 py-1.5 rounded-lg text-xs font-semibold flex items-center space-x-1 text-slate-400 hover:text-white transition"
            title="Switch to Joltrin Technical Demo"
          >
            <span>🧠 Tech Demo</span>
          </a>

          <button
            onClick={() => onSelectMode('arena')}
            className={`px-3 py-1.5 rounded-lg text-xs font-semibold flex items-center space-x-1.5 transition ${
              currentMode === 'arena'
                ? 'bg-brand-500/20 text-brand-400 border border-brand-500/30'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <Flame className="w-3.5 h-3.5" />
            <span>Arena</span>
          </button>

          <button
            onClick={() => onSelectMode('investor')}
            className={`px-3 py-1.5 rounded-lg text-xs font-semibold flex items-center space-x-1.5 transition ${
              currentMode === 'investor'
                ? 'bg-accent-cyan/20 text-accent-cyan border border-accent-cyan/30'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <Briefcase className="w-3.5 h-3.5" />
            <span>Investor Mode</span>
          </button>

          <button
            onClick={onOpenCompare}
            className="px-3 py-1.5 rounded-lg text-xs font-semibold flex items-center space-x-1.5 text-slate-400 hover:text-white transition"
          >
            <GitCompare className="w-3.5 h-3.5" />
            <span>With vs Without</span>
          </button>

          <a
            href="../agents/"
            className="px-2.5 py-1.5 rounded-lg text-xs font-semibold flex items-center space-x-1 text-slate-400 hover:text-white transition"
            title="Switch to Joltrin Agent Verification Barrier"
          >
            <span>🔌 Barrier</span>
          </a>

          <button
            onClick={() => onOpenEnterprise('enterprise')}
            className="px-2.5 py-1.5 rounded-lg text-xs font-semibold flex items-center space-x-1 text-brand-400 hover:text-brand-300 transition"
            title="Commercial Editions & Enterprise Inquiries"
          >
            <span>🏢 Enterprise</span>
          </button>
        </div>

        {/* Action Controls & External Links */}
        <div className="flex items-center space-x-2.5">
          
          {/* Enterprise Inquiry Trigger */}
          <button
            onClick={() => onOpenEnterprise('enterprise')}
            className="hidden sm:flex items-center space-x-1.5 px-3 py-1.5 rounded-lg bg-dark-900 hover:bg-dark-850 text-brand-400 border border-brand-500/30 text-xs font-semibold shadow-sm transition"
            title="Deploy Joltrin in Production"
          >
            <Building2 className="w-3.5 h-3.5" />
            <span>Enterprise</span>
          </button>
          
          {/* Reliability Score Live Capsule */}
          <div className="hidden lg:flex items-center space-x-2 bg-dark-900 px-3 py-1.5 rounded-lg border border-dark-800 font-mono text-xs">
            <span className="text-slate-400">Reliability:</span>
            <span className={`font-bold ${reliabilityScore > 90 ? 'text-brand-400' : 'text-amber-400'}`}>
              {reliabilityScore.toFixed(1)}%
            </span>
          </div>

          {/* Copilot Trigger */}
          <button
            onClick={onOpenCopilot}
            className="px-3 py-1.5 rounded-lg bg-dark-900 hover:bg-dark-850 text-accent-cyan border border-accent-cyan/30 text-xs font-semibold flex items-center space-x-1.5 shadow-sm transition"
            title="Ask Joltrin Copilot"
          >
            <Sparkles className="w-3.5 h-3.5" />
            <span className="hidden sm:inline">Copilot</span>
          </button>

          {/* Sound Toggle */}
          <button
            onClick={toggleSound}
            className="p-2 rounded-lg bg-dark-900 hover:bg-dark-850 text-slate-400 hover:text-white border border-dark-800 transition"
            title={isMuted ? 'Unmute Sound FX' : 'Mute Sound FX'}
          >
            {isMuted ? <VolumeX className="w-4 h-4" /> : <Volume2 className="w-4 h-4 text-brand-400" />}
          </button>

          {/* Share Link */}
          <button
            onClick={handleShare}
            className="p-2 rounded-lg bg-dark-900 hover:bg-dark-850 text-slate-400 hover:text-white border border-dark-800 transition"
            title="Challenge a teammate / Share Demo"
          >
            {copied ? <Check className="w-4 h-4 text-brand-400" /> : <Share2 className="w-4 h-4" />}
          </button>

          {/* GitHub Repo Link: hidden below sm, next to Copilot/Sound/Share it
              was crowding the hamburger button out on narrow phones. Still
              reachable from the mobile panel below. */}
          <a
            href="https://github.com/sharedcode/joltrin"
            target="_blank"
            rel="noopener noreferrer"
            className="hidden sm:flex items-center space-x-1.5 px-3 py-1.5 rounded-lg bg-brand-500 hover:bg-brand-400 text-black font-semibold text-xs transition shadow-md shadow-brand-500/20"
          >
            <GithubIcon className="w-4 h-4 text-black" />
            <span className="hidden sm:inline">GitHub</span>
          </a>

          {/* Mobile Menu Toggle: the nav switcher above is md:flex-only, this is
              the only way to reach Tech Demo, Barrier, Investor Mode, Compare,
              or Enterprise below that breakpoint. */}
          <button
            onClick={() => setMobileMenuOpen((open) => !open)}
            className="md:hidden p-2 rounded-lg bg-dark-900 hover:bg-dark-850 text-slate-300 hover:text-white border border-dark-800 transition"
            aria-label="Toggle navigation menu"
            aria-expanded={mobileMenuOpen}
          >
            {mobileMenuOpen ? <X className="w-4 h-4" /> : <Menu className="w-4 h-4" />}
          </button>
        </div>

      </div>

      {/* Mobile Nav Panel: same destinations as the desktop switcher above,
          stacked for a narrow viewport. */}
      {mobileMenuOpen && (
        <div className="md:hidden border-t border-dark-800 bg-dark-950 px-4 py-3 space-y-1 text-sm font-mono">
          <a
            href="../"
            className="block px-3 py-2 rounded-lg text-slate-300 hover:text-white hover:bg-dark-900 transition"
          >
            🧠 Tech Demo
          </a>
          <button
            onClick={() => { onSelectMode('arena'); setMobileMenuOpen(false); }}
            className="block w-full text-left px-3 py-2 rounded-lg text-slate-300 hover:text-white hover:bg-dark-900 transition"
          >
            🔥 Arena
          </button>
          <button
            onClick={() => { onSelectMode('investor'); setMobileMenuOpen(false); }}
            className="block w-full text-left px-3 py-2 rounded-lg text-slate-300 hover:text-white hover:bg-dark-900 transition"
          >
            💼 Investor Mode
          </button>
          <button
            onClick={() => { onOpenCompare(); setMobileMenuOpen(false); }}
            className="block w-full text-left px-3 py-2 rounded-lg text-slate-300 hover:text-white hover:bg-dark-900 transition"
          >
            ⚖️ With vs Without
          </button>
          <a
            href="../agents/"
            className="block px-3 py-2 rounded-lg text-slate-300 hover:text-white hover:bg-dark-900 transition"
          >
            🔌 Barrier
          </a>
          <button
            onClick={() => { onOpenEnterprise('enterprise'); setMobileMenuOpen(false); }}
            className="block w-full text-left px-3 py-2 rounded-lg text-brand-400 hover:text-brand-300 hover:bg-dark-900 transition"
          >
            🏢 Enterprise
          </button>
          <a
            href="https://github.com/sharedcode/joltrin"
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center space-x-1.5 px-3 py-2 rounded-lg text-slate-300 hover:text-white hover:bg-dark-900 transition border-t border-dark-800 mt-1 pt-3"
          >
            <GithubIcon className="w-4 h-4" />
            <span>GitHub</span>
          </a>
        </div>
      )}
    </header>
  );
};
