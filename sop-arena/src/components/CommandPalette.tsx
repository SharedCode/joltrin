import React, { useEffect, useMemo, useState } from 'react';
import { Search } from 'lucide-react';
import { ViewMode } from '../types';

interface CommandPaletteProps {
  isOpen: boolean;
  onClose: () => void;
  onOpen: () => void;
  onSelectMode: (mode: ViewMode) => void;
  onOpenCompare: () => void;
  onOpenEnterprise: (tier?: 'pro' | 'enterprise') => void;
  onOpenCopilot: () => void;
}

interface Command {
  label: string;
  hint: string;
  action: () => void;
}

export const CommandPalette: React.FC<CommandPaletteProps> = ({
  isOpen,
  onClose,
  onOpen,
  onSelectMode,
  onOpenCompare,
  onOpenEnterprise,
  onOpenCopilot,
}) => {
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState(0);

  const commands: Command[] = useMemo(
    () => [
      { label: '🔥 Arena Simulation', hint: 'mode', action: () => onSelectMode('arena') },
      { label: '💼 Investor Mode', hint: 'mode', action: () => onSelectMode('investor') },
      { label: '⚖️ With vs Without Joltrin', hint: 'action', action: onOpenCompare },
      { label: '✨ Ask Joltrin Copilot', hint: 'action', action: onOpenCopilot },
      { label: '🏢 Enterprise Access', hint: 'action', action: () => onOpenEnterprise('enterprise') },
      { label: '🧠 Technical Demo & Engine', hint: 'go to', action: () => { window.location.href = '../'; } },
      { label: '🔌 Agent Verification Barrier', hint: 'go to', action: () => { window.location.href = '../agents/'; } },
      { label: 'GitHub Repository', hint: 'external', action: () => window.open('https://github.com/sharedcode/joltrin', '_blank', 'noopener') },
    ],
    [onSelectMode, onOpenCompare, onOpenCopilot, onOpenEnterprise]
  );

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q === '' ? commands : commands.filter((c) => c.label.toLowerCase().includes(q));
  }, [commands, query]);

  useEffect(() => {
    if (isOpen) {
      setQuery('');
      setSelected(0);
    }
  }, [isOpen]);

  useEffect(() => {
    setSelected(0);
  }, [query]);

  useEffect(() => {
    const handleGlobalKeydown = (event: KeyboardEvent) => {
      const isMac = navigator.platform.toUpperCase().indexOf('MAC') >= 0;
      const modifierHeld = isMac ? event.metaKey : event.ctrlKey;
      if (modifierHeld && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        isOpen ? onClose() : onOpen();
      } else if (event.key === 'Escape' && isOpen) {
        onClose();
      }
    };
    document.addEventListener('keydown', handleGlobalKeydown);
    return () => document.removeEventListener('keydown', handleGlobalKeydown);
  }, [isOpen, onOpen, onClose]);

  if (!isOpen) return null;

  const runCommand = (cmd: Command) => {
    cmd.action();
    onClose();
  };

  const handleInputKeydown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      setSelected((s) => Math.min(s + 1, filtered.length - 1));
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      setSelected((s) => Math.max(s - 1, 0));
    } else if (event.key === 'Enter') {
      event.preventDefault();
      const cmd = filtered[selected];
      if (cmd) runCommand(cmd);
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 bg-dark-950/80 backdrop-blur-sm flex items-start justify-center pt-24 px-4"
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div className="w-full max-w-lg bg-dark-900 border border-dark-700 rounded-2xl shadow-2xl overflow-hidden">
        <div className="flex items-center px-4 border-b border-dark-800">
          <Search className="w-4 h-4 text-slate-500 shrink-0" />
          <input
            autoFocus
            type="text"
            value={query}
            placeholder="Jump to a mode or action..."
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={handleInputKeydown}
            autoComplete="off"
            className="w-full bg-transparent px-3 py-3.5 text-sm text-white placeholder-slate-500 focus:outline-none font-mono"
          />
          <kbd className="text-[10px] text-slate-500 border border-dark-700 rounded px-1.5 py-0.5 font-mono shrink-0">ESC</kbd>
        </div>
        <div className="max-h-80 overflow-y-auto p-2">
          {filtered.length === 0 ? (
            <div className="px-4 py-6 text-center text-sm text-slate-500 font-mono">No matches</div>
          ) : (
            filtered.map((cmd, i) => (
              <button
                key={cmd.label}
                type="button"
                onMouseEnter={() => setSelected(i)}
                onClick={() => runCommand(cmd)}
                className={`w-full text-left px-3 py-2.5 rounded-lg flex items-center justify-between gap-3 text-sm transition ${
                  i === selected ? 'bg-brand-500/20 text-brand-300' : 'text-slate-300 hover:bg-dark-800'
                }`}
              >
                <span>{cmd.label}</span>
                <span className="text-[10px] text-slate-500 font-mono uppercase">{cmd.hint}</span>
              </button>
            ))
          )}
        </div>
      </div>
    </div>
  );
};
