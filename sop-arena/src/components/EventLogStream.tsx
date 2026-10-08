import React, { useState, useRef, useEffect } from 'react';
import { LogEntry, LogCategory } from '../types';
import { Terminal, Filter, Trash2, ArrowDown } from 'lucide-react';

interface EventLogStreamProps {
  logs: LogEntry[];
  onClearLogs?: () => void;
}

export const EventLogStream: React.FC<EventLogStreamProps> = ({ logs, onClearLogs }) => {
  const [filter, setFilter] = useState<string>('ALL');
  const [autoScroll, setAutoScroll] = useState<boolean>(true);
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const filteredLogs = logs.filter((l) => {
    if (filter === 'ALL') return true;
    return l.category === filter;
  });

  useEffect(() => {
    if (autoScroll && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [logs, autoScroll]);

  return (
    <div className="bg-dark-900 border border-dark-800 rounded-2xl p-5 flex flex-col h-[380px]">
      
      {/* Header & Controls */}
      <div className="flex flex-wrap items-center justify-between gap-2 pb-3 mb-3 border-b border-dark-800">
        <div className="flex items-center space-x-2">
          <Terminal className="w-4 h-4 text-brand-400" />
          <h2 className="text-xs font-bold text-white uppercase tracking-wider font-mono">
            Live Distributed Event Stream
          </h2>
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-dark-800 text-slate-400 font-mono">
            {logs.length} events
          </span>
        </div>

        {/* Filter Pills */}
        <div className="flex items-center space-x-1 text-[10px] font-mono">
          {['ALL', 'TX', 'SWARM', 'FAILOVER', 'RECOVERY'].map((cat) => (
            <button
              key={cat}
              onClick={() => setFilter(cat)}
              className={`px-2 py-0.5 rounded transition ${
                filter === cat
                  ? 'bg-brand-500/20 text-brand-400 font-bold border border-brand-500/30'
                  : 'text-slate-400 hover:text-white bg-dark-950 border border-transparent'
              }`}
            >
              {cat}
            </button>
          ))}

          {onClearLogs && (
            <button
              onClick={onClearLogs}
              className="p-1 text-slate-500 hover:text-slate-300 ml-1 transition"
              title="Clear event stream"
            >
              <Trash2 className="w-3 h-3" />
            </button>
          )}
        </div>
      </div>

      {/* Terminal Log Window */}
      <div
        ref={scrollRef}
        className="flex-grow overflow-y-auto space-y-1.5 font-mono text-[11px] leading-relaxed text-slate-300 pr-2 terminal-scroll"
      >
        {filteredLogs.length === 0 ? (
          <div className="text-slate-600 text-center py-10">// No events matching filter.</div>
        ) : (
          filteredLogs.map((log) => {
            let badgeColor = 'bg-dark-800 text-slate-400';
            let textColor = 'text-slate-300';

            if (log.level === 'error') {
              badgeColor = 'bg-rose-500/20 text-rose-400 border border-rose-500/30';
              textColor = 'text-rose-300 font-semibold';
            } else if (log.level === 'warn') {
              badgeColor = 'bg-amber-500/20 text-amber-400 border border-amber-500/30';
              textColor = 'text-amber-300';
            } else if (log.level === 'sop') {
              badgeColor = 'bg-brand-500/20 text-brand-400 border border-brand-500/30';
              textColor = 'text-brand-300 font-semibold';
            } else if (log.level === 'success') {
              badgeColor = 'bg-emerald-500/20 text-emerald-400 border border-emerald-500/30';
              textColor = 'text-emerald-300 font-medium';
            }

            return (
              <div key={log.id} className="flex items-start space-x-2 py-0.5 hover:bg-dark-850/50 rounded px-1 transition">
                <span className="text-[10px] text-slate-500 flex-shrink-0 select-none">{log.timestamp}</span>
                <span className={`text-[9px] px-1.5 py-0.2 rounded font-bold uppercase flex-shrink-0 select-none ${badgeColor}`}>
                  {log.category}
                </span>
                <span className={`flex-grow break-words ${textColor}`}>{log.message}</span>
              </div>
            );
          })
        )}
      </div>

      {/* Auto-scroll toggle indicator */}
      <div className="pt-2 mt-2 border-t border-dark-800 flex items-center justify-between text-[10px] font-mono text-slate-500">
        <span className="flex items-center space-x-1.5">
          <span className="w-1.5 h-1.5 rounded-full bg-brand-400 animate-pulse"></span>
          <span>Observability telemetry streaming in 60fps</span>
        </span>
        <button
          onClick={() => setAutoScroll(!autoScroll)}
          className={`hover:text-slate-300 flex items-center space-x-1 ${autoScroll ? 'text-brand-400' : 'text-slate-500'}`}
        >
          <ArrowDown className="w-3 h-3" />
          <span>Auto-scroll {autoScroll ? 'ON' : 'OFF'}</span>
        </button>
      </div>

    </div>
  );
};
