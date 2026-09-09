import React, { useRef, useEffect } from 'react';
import { TopologyNode, JobParticle } from '../types';
import { Database, Cpu, Server, Activity, ShieldAlert, Sparkles, Layers } from 'lucide-react';

interface TopologyGraphProps {
  nodes: TopologyNode[];
  particles: JobParticle[];
  onSelectNode?: (node: TopologyNode) => void;
  selectedNodeId?: string | null;
}

export const TopologyGraph: React.FC<TopologyGraphProps> = ({
  nodes,
  particles,
  onSelectNode,
  selectedNodeId,
}) => {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    let animationFrameId: number;

    const render = () => {
      const width = canvas.clientWidth;
      const height = canvas.clientHeight;
      canvas.width = width * window.devicePixelRatio;
      canvas.height = height * window.devicePixelRatio;
      ctx.scale(window.devicePixelRatio, window.devicePixelRatio);

      ctx.clearRect(0, 0, width, height);

      // Map node IDs to canvas coordinates
      const nodeCoords: Record<string, { x: number; y: number; node: TopologyNode }> = {};
      nodes.forEach((n) => {
        nodeCoords[n.id] = {
          x: (n.x / 100) * width,
          y: (n.y / 100) * height,
          node: n,
        };
      });

      // 1. Draw Network Connection Edges
      const client = nodeCoords['node-client'];
      const coord = nodeCoords['node-coord'];

      if (client && coord) {
        drawCurvedEdge(ctx, client.x, client.y, coord.x, coord.y, 'rgba(59, 130, 246, 0.25)');
      }

      const workers = nodes.filter((n) => n.type === 'worker');
      const storage = nodes.filter((n) => n.type === 'storage');

      if (coord) {
        workers.forEach((w) => {
          const wCoord = nodeCoords[w.id];
          if (wCoord) {
            const color = w.status === 'failed' ? 'rgba(244, 63, 94, 0.15)' : 'rgba(16, 185, 129, 0.25)';
            drawCurvedEdge(ctx, coord.x, coord.y, wCoord.x, wCoord.y, color);
          }
        });
      }

      workers.forEach((w) => {
        const wCoord = nodeCoords[w.id];
        if (wCoord) {
          storage.forEach((s) => {
            const sCoord = nodeCoords[s.id];
            if (sCoord) {
              const color = s.status === 'failed' ? 'rgba(244, 63, 94, 0.15)' : 'rgba(6, 182, 212, 0.2)';
              drawCurvedEdge(ctx, wCoord.x, wCoord.y, sCoord.x, sCoord.y, color);
            }
          });
        }
      });

      // 2. Draw Traveling Job Particles
      particles.forEach((p) => {
        const from = nodeCoords[p.fromId];
        const to = nodeCoords[p.toId];
        if (!from || !to) return;

        // Quadratic bezier midpoint
        const midX = (from.x + to.x) / 2;
        const midY = (from.y + to.y) / 2 - 15;

        // Calculate position along curve using t = p.progress
        const t = p.progress;
        const px = (1 - t) * (1 - t) * from.x + 2 * (1 - t) * t * midX + t * t * to.x;
        const py = (1 - t) * (1 - t) * from.y + 2 * (1 - t) * t * midY + t * t * to.y;

        let color = '#34d399'; // default emerald write
        let radius = 3.5;
        if (p.type === 'ai_task') {
          color = '#06b6d4'; // cyan
          radius = 4;
        } else if (p.type === 'rebalance' || p.type === 'recovery') {
          color = '#8b5cf6'; // violet
          radius = 4.5;
        }

        // Particle Glow & Core
        ctx.beginPath();
        ctx.arc(px, py, radius + 2, 0, Math.PI * 2);
        ctx.fillStyle = color.replace(')', ', 0.3)').replace('#', 'rgba(');
        ctx.fill();

        ctx.beginPath();
        ctx.arc(px, py, radius, 0, Math.PI * 2);
        ctx.fillStyle = color;
        ctx.shadowColor = color;
        ctx.shadowBlur = 8;
        ctx.fill();
        ctx.shadowBlur = 0;
      });

      animationFrameId = requestAnimationFrame(render);
    };

    render();

    return () => {
      cancelAnimationFrame(animationFrameId);
    };
  }, [nodes, particles]);

  const drawCurvedEdge = (
    ctx: CanvasRenderingContext2D,
    x1: number,
    y1: number,
    x2: number,
    y2: number,
    strokeColor: string
  ) => {
    ctx.beginPath();
    ctx.moveTo(x1, y1);
    const midX = (x1 + x2) / 2;
    const midY = (y1 + y2) / 2 - 15;
    ctx.quadraticCurveTo(midX, midY, x2, y2);
    ctx.strokeStyle = strokeColor;
    ctx.lineWidth = 1.5;
    ctx.stroke();
  };

  return (
    <div className="relative w-full h-[480px] bg-dark-900 border border-dark-800 rounded-2xl overflow-hidden shadow-2xl">
      
      {/* Background Subtle Grid */}
      <div 
        className="absolute inset-0 opacity-20 pointer-events-none"
        style={{
          backgroundImage: 'radial-gradient(rgba(255, 255, 255, 0.15) 1px, transparent 1px)',
          backgroundSize: '24px 24px'
        }}
      />

      {/* Layer Labels */}
      <div className="absolute top-4 left-6 z-10 flex items-center space-x-6 text-[10px] font-mono uppercase tracking-wider text-slate-500 pointer-events-none">
        <span className="flex items-center space-x-1.5"><Activity className="w-3.5 h-3.5 text-blue-400" /><span>1. Ingestion Tier</span></span>
        <span className="flex items-center space-x-1.5"><Sparkles className="w-3.5 h-3.5 text-brand-400" /><span>2. Joltrin Unified Engine</span></span>
        <span className="flex items-center space-x-1.5"><Cpu className="w-3.5 h-3.5 text-accent-cyan" /><span>3. Swarm Compute</span></span>
        <span className="flex items-center space-x-1.5"><Database className="w-3.5 h-3.5 text-accent-violet" /><span>4. B-Tree Shards</span></span>
      </div>

      {/* Canvas for dynamic particles & edges */}
      <canvas ref={canvasRef} className="absolute inset-0 w-full h-full pointer-events-none z-10" />

      {/* DOM Interactive Node Cards */}
      <div className="absolute inset-0 z-20 pointer-events-auto">
        {nodes.map((node) => {
          const isSelected = selectedNodeId === node.id;
          const isFailed = node.status === 'failed';
          const isRecovering = node.status === 'recovering';
          const isScaling = node.status === 'scaling';

          let borderClass = 'border-dark-700 bg-dark-950/90';
          let glowClass = '';
          let iconColor = 'text-brand-400';

          if (isFailed) {
            borderClass = 'border-rose-500/80 bg-rose-950/80 animate-pulse';
            glowClass = 'shadow-lg shadow-rose-500/30';
            iconColor = 'text-rose-400';
          } else if (isRecovering) {
            borderClass = 'border-accent-cyan/80 bg-cyan-950/80 animate-pulse';
            glowClass = 'shadow-lg shadow-cyan-500/30';
            iconColor = 'text-accent-cyan';
          } else if (isScaling) {
            borderClass = 'border-amber-400/80 bg-amber-950/80';
            iconColor = 'text-amber-400';
          } else if (node.type === 'coordinator') {
            borderClass = 'border-brand-500/60 bg-dark-850/95';
            glowClass = 'shadow-xl shadow-brand-500/20 ring-1 ring-brand-500/40';
            iconColor = 'text-brand-400';
          }

          return (
            <div
              key={node.id}
              onClick={() => onSelectNode && onSelectNode(node)}
              style={{
                left: `${node.x}%`,
                top: `${node.y}%`,
                transform: 'translate(-50%, -50%)',
              }}
              className={`absolute cursor-pointer rounded-xl border p-2.5 transition-all duration-300 hover:scale-105 select-none ${borderClass} ${glowClass} ${
                isSelected ? 'ring-2 ring-brand-400 scale-105' : ''
              } ${node.type === 'coordinator' ? 'w-44' : 'w-36 sm:w-40'}`}
            >
              <div className="flex items-center justify-between mb-1.5">
                <div className="flex items-center space-x-1.5 truncate">
                  {node.type === 'coordinator' && <Sparkles className={`w-3.5 h-3.5 ${iconColor}`} />}
                  {node.type === 'storage' && <Database className={`w-3.5 h-3.5 ${iconColor}`} />}
                  {node.type === 'worker' && <Cpu className={`w-3.5 h-3.5 ${iconColor}`} />}
                  {node.type === 'client' && <Activity className={`w-3.5 h-3.5 ${iconColor}`} />}
                  <span className="text-[11px] font-bold text-white truncate">{node.name}</span>
                </div>
                
                {/* Status Dot */}
                <span
                  className={`w-2 h-2 rounded-full ${
                    isFailed
                      ? 'bg-rose-500 animate-ping'
                      : isRecovering
                      ? 'bg-accent-cyan animate-pulse'
                      : isScaling
                      ? 'bg-amber-400 animate-spin'
                      : 'bg-brand-400'
                  }`}
                />
              </div>

              {/* Mini Status / Load Metric */}
              <div className="text-[9px] font-mono text-slate-400 flex items-center justify-between mb-1">
                <span>{isFailed ? 'FAILED' : isRecovering ? 'RECOVERING' : `${node.activeTasks} tasks`}</span>
                <span>{node.load}% load</span>
              </div>

              {/* Mini Progress Bar */}
              <div className="w-full bg-dark-800 h-1 rounded-full overflow-hidden">
                <div
                  className={`h-full transition-all duration-300 ${
                    isFailed
                      ? 'bg-rose-500 w-full'
                      : isRecovering
                      ? 'bg-accent-cyan w-3/4'
                      : node.load > 80
                      ? 'bg-amber-400'
                      : 'bg-brand-500'
                  }`}
                  style={{ width: isFailed ? '100%' : `${node.load}%` }}
                />
              </div>

              {node.shards && (
                <div className="mt-1 pt-1 border-t border-dark-800/80 flex items-center justify-between text-[8px] font-mono text-slate-400 truncate">
                  <span className="truncate">{node.shards[0]}</span>
                  <span className="text-accent-cyan">EC:OK</span>
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* Legend Footer */}
      <div className="absolute bottom-3 right-4 z-20 flex items-center space-x-3 bg-dark-950/80 px-3 py-1.5 rounded-lg border border-dark-800 text-[10px] font-mono text-slate-400">
        <span className="flex items-center space-x-1"><span className="w-2 h-2 rounded-full bg-brand-400 inline-block"></span><span>Healthy</span></span>
        <span className="flex items-center space-x-1"><span className="w-2 h-2 rounded-full bg-amber-400 inline-block"></span><span>Scaling</span></span>
        <span className="flex items-center space-x-1"><span className="w-2 h-2 rounded-full bg-rose-500 inline-block"></span><span>Offline / Failover</span></span>
        <span className="flex items-center space-x-1"><span className="w-2 h-2 rounded-full bg-accent-cyan inline-block"></span><span>Erasure Rebuild</span></span>
      </div>

    </div>
  );
};
