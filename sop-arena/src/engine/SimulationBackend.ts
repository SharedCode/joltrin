import { TopologyNode, SystemMetrics, LogEntry, JobParticle, DataRecord, LogCategory, LogLevel } from '../types';
import { SopBackendInterface, SopBackendListener } from './SopBackendInterface';
import { sounds } from './SoundEffects';

export class SimulationBackend implements SopBackendInterface {
  private listener: SopBackendListener | null = null;
  private timer: number | null = null;
  private particleTimer: number | null = null;

  private nodes: TopologyNode[] = [];
  private particles: JobParticle[] = [];
  private records: DataRecord[] = [];
  private metrics: SystemMetrics = {
    tps: 12500,
    totalTransactions: 142800,
    consistencyRate: 100.00,
    activeWorkers: 6,
    totalWorkers: 6,
    activeStorageNodes: 4,
    totalStorageNodes: 4,
    avgLatencyMs: 0.28,
    p99LatencyMs: 0.84,
    reliabilityScore: 98.6,
    redistributedJobs: 0,
    erasureChunksRecovered: 0,
    conflictsResolved: 0,
    glueComponentsEliminated: 6,
  };

  private targetTps: number = 15000;
  private particleIdCounter = 0;
  private logIdCounter = 0;
  private instanceId = Date.now().toString(36) + Math.random().toString(36).slice(2, 6);

  constructor() {
    this.resetTopology();
    this.seedRecords();
  }

  public init(listener: SopBackendListener) {
    this.listener = listener;
    this.broadcastState();

    // Main simulation tick (every 250ms for metrics & recovery)
    this.timer = window.setInterval(() => {
      this.simulationTick();
    }, 250);

    // High frequency particle physics tick (every 30ms for 60fps canvas)
    this.particleTimer = window.setInterval(() => {
      this.particleTick();
    }, 32);

    this.addLog('SYSTEM', 'info', 'Joltrin Simulation Engine initialized. Zero-master coordinator active.');
  }

  public destroy() {
    if (this.timer) clearInterval(this.timer);
    if (this.particleTimer) clearInterval(this.particleTimer);
    this.listener = null;
  }

  private resetTopology() {
    this.nodes = [
      // Client Ingestion Gateway (Left)
      {
        id: 'node-client',
        type: 'client',
        name: 'Ingestion Gateway',
        status: 'healthy',
        load: 45,
        activeTasks: 180,
        capacity: 50000,
        x: 12,
        y: 48,
        memoryMb: 128,
        roleDescription: 'Edge API Ingestion point'
      },
      // Joltrin Unified Coordinator (Center-Left)
      {
        id: 'node-coord',
        type: 'coordinator',
        name: 'Joltrin Unified Kernel',
        status: 'healthy',
        load: 38,
        activeTasks: 350,
        capacity: 100000,
        x: 35,
        y: 48,
        memoryMb: 256,
        roleDescription: 'Zero-master ACID coordinator & OCC latch'
      },
      // Compute Workers (Top Right / Middle Right)
      { id: 'worker-1', type: 'worker', name: 'Agent Worker 01', status: 'healthy', load: 52, activeTasks: 42, capacity: 500, x: 62, y: 16, memoryMb: 512, roleDescription: 'Swarm Task Processing' },
      { id: 'worker-2', type: 'worker', name: 'Agent Worker 02', status: 'healthy', load: 48, activeTasks: 39, capacity: 500, x: 62, y: 34, memoryMb: 512, roleDescription: 'Swarm Task Processing' },
      { id: 'worker-3', type: 'worker', name: 'Agent Worker 03', status: 'healthy', load: 60, activeTasks: 51, capacity: 500, x: 62, y: 52, memoryMb: 512, roleDescription: 'Swarm Task Processing' },
      { id: 'worker-4', type: 'worker', name: 'Agent Worker 04', status: 'healthy', load: 44, activeTasks: 36, capacity: 500, x: 62, y: 70, memoryMb: 512, roleDescription: 'Swarm Task Processing' },
      { id: 'worker-5', type: 'worker', name: 'Agent Worker 05', status: 'healthy', load: 38, activeTasks: 28, capacity: 500, x: 62, y: 86, memoryMb: 512, roleDescription: 'Swarm Task Processing' },
      { id: 'worker-6', type: 'worker', name: 'Agent Worker 06', status: 'healthy', load: 55, activeTasks: 45, capacity: 500, x: 74, y: 48, memoryMb: 512, roleDescription: 'Swarm Task Processing' },

      // B-Tree Storage Shards (Far Right)
      { id: 'store-1', type: 'storage', name: 'B-Tree Shard #01', status: 'healthy', load: 62, activeTasks: 120, capacity: 2000, x: 88, y: 18, memoryMb: 1024, shards: ['Keys 000-249', 'Erasure Parity A'] },
      { id: 'store-2', type: 'storage', name: 'B-Tree Shard #02', status: 'healthy', load: 58, activeTasks: 110, capacity: 2000, x: 88, y: 38, memoryMb: 1024, shards: ['Keys 250-499', 'Erasure Parity B'] },
      { id: 'store-3', type: 'storage', name: 'B-Tree Shard #03', status: 'healthy', load: 64, activeTasks: 125, capacity: 2000, x: 88, y: 60, memoryMb: 1024, shards: ['Keys 500-749', 'Erasure Parity C'] },
      { id: 'store-4', type: 'storage', name: 'B-Tree Shard #04', status: 'healthy', load: 54, activeTasks: 95, capacity: 2000, x: 88, y: 82, memoryMb: 1024, shards: ['Keys 750-999', 'Erasure Parity D'] },
    ];
  }

  private seedRecords() {
    this.records = [
      { key: 'agent:ctx:701', value: { agent: 'SearchBot-9', state: 'RUNNING', memSlots: 128 }, version: 4, shard: 'store-1', updatedAt: 'Just now' },
      { key: 'ledger:acc:001', value: { holder: 'Acme Treasury', balance: 5000000.00, txSeq: 108 }, version: 12, shard: 'store-2', updatedAt: 'Just now' },
      { key: 'game:player:402', value: { pos: [142.5, 88.1, 12.0], inventory: 48, hp: 100 }, version: 89, shard: 'store-3', updatedAt: 'Just now' },
      { key: 'vector:embed:91', value: { dims: 128, norm: 1.0, label: 'ACID Coordination' }, version: 1, shard: 'store-4', updatedAt: 'Just now' },
      { key: 'swarm:task:8192', value: { status: 'COMMITTED', retries: 0, assignedTo: 'worker-2' }, version: 2, shard: 'store-1', updatedAt: 'Just now' },
    ];
  }

  public setTargetTps(tps: number) {
    this.targetTps = tps;
    this.addLog('SYSTEM', 'info', `Target workload set to ${tps.toLocaleString()} ops/sec`);
    sounds.playClick();
  }

  public createTransactionStorm(multiplier: number = 3) {
    this.targetTps = Math.min(100000, this.targetTps * multiplier);
    this.metrics.conflictsResolved += Math.floor(Math.random() * 45) + 15;
    this.addLog('TX', 'warn', `⚡ Transaction Storm initiated! Workload spiked to ${this.targetTps.toLocaleString()} TPS.`);
    this.addLog('TX', 'sop', `Joltrin Optimistic Concurrency Control (OCC) resolving lock contentions in <0.5ms.`);
    sounds.playAlarm();
    
    // Spawn burst of particles
    for (let i = 0; i < 15; i++) {
      this.spawnParticle('tx_write');
    }
  }

  public addWorker() {
    const nextIdx = this.nodes.filter(n => n.type === 'worker').length + 1;
    if (nextIdx > 10) {
      this.addLog('SWARM', 'warn', 'Maximum worker swarm capacity reached.');
      return;
    }
    const newWorker: TopologyNode = {
      id: `worker-${nextIdx}`,
      type: 'worker',
      name: `Agent Worker ${nextIdx < 10 ? '0' + nextIdx : nextIdx}`,
      status: 'scaling',
      load: 10,
      activeTasks: 0,
      capacity: 500,
      x: 62 + (nextIdx % 2 === 0 ? 12 : 0),
      y: Math.min(90, 15 + (nextIdx - 1) * 12),
      memoryMb: 512,
      roleDescription: 'Swarm Task Processing'
    };
    this.nodes.push(newWorker);
    this.metrics.totalWorkers = this.nodes.filter(n => n.type === 'worker').length;
    this.metrics.activeWorkers = this.nodes.filter(n => n.type === 'worker' && n.status === 'healthy').length;

    this.addLog('SWARM', 'sop', `Worker ${newWorker.name} joined swarm. Auto-rebalancing compute queue.`);
    sounds.playCommit();

    setTimeout(() => {
      newWorker.status = 'healthy';
      this.metrics.activeWorkers = this.nodes.filter(n => n.type === 'worker' && n.status === 'healthy').length;
      this.broadcastState();
    }, 600);

    this.broadcastState();
  }

  public removeWorker(workerId?: string) {
    const workers = this.nodes.filter(n => n.type === 'worker' && n.status !== 'failed');
    if (workers.length <= 2) {
      this.addLog('SWARM', 'warn', 'Cannot remove worker: Swarm requires minimum 2 active nodes.');
      return;
    }
    const target = workerId ? this.nodes.find(n => n.id === workerId) : workers[workers.length - 1];
    if (!target) return;

    const redistributed = target.activeTasks;
    this.nodes = this.nodes.filter(n => n.id !== target.id);
    this.metrics.redistributedJobs += redistributed;
    this.metrics.totalWorkers = this.nodes.filter(n => n.type === 'worker').length;
    this.metrics.activeWorkers = this.nodes.filter(n => n.type === 'worker' && n.status === 'healthy').length;

    this.addLog('SWARM', 'sop', `Worker ${target.name} gracefully removed. ${redistributed} tasks redistributed with zero dropped writes.`);
    sounds.playClick();
    this.broadcastState();
  }

  public killWorker(workerId?: string) {
    const healthyWorkers = this.nodes.filter(n => n.type === 'worker' && n.status === 'healthy');
    if (healthyWorkers.length === 0) return;

    const target = workerId ? this.nodes.find(n => n.id === workerId) : healthyWorkers[Math.floor(Math.random() * healthyWorkers.length)];
    if (!target || target.status === 'failed') return;

    target.status = 'failed';
    target.failTime = Date.now();
    const lostTasks = target.activeTasks;
    target.activeTasks = 0;
    target.load = 0;

    this.metrics.activeWorkers = this.nodes.filter(n => n.type === 'worker' && n.status === 'healthy').length;
    this.metrics.redistributedJobs += lostTasks;

    this.addLog('FAILOVER', 'error', `⚠️ WORKER FAILURE: ${target.name} crashed abruptly!`);
    this.addLog('FAILOVER', 'sop', `Joltrin Heartbeat detected timeout in 12ms. Redistributed ${lostTasks} uncommitted tasks to surviving swarm.`);
    sounds.playAlarm();

    // Auto heal after 4.5 seconds
    setTimeout(() => {
      if (target.status === 'failed') {
        target.status = 'recovering';
        this.addLog('RECOVERY', 'info', `${target.name} restarting container runtime...`);
        this.broadcastState();

        setTimeout(() => {
          target.status = 'healthy';
          target.load = 40;
          this.metrics.activeWorkers = this.nodes.filter(n => n.type === 'worker' && n.status === 'healthy').length;
          this.addLog('RECOVERY', 'success', `✓ ${target.name} re-joined swarm. State synchronized.`);
          sounds.playRecovery();
          this.broadcastState();
        }, 1200);
      }
    }, 4500);

    this.broadcastState();
  }

  public failStorageNode(nodeId?: string) {
    const healthyStorage = this.nodes.filter(n => n.type === 'storage' && n.status === 'healthy');
    if (healthyStorage.length === 0) return;

    const target = nodeId ? this.nodes.find(n => n.id === nodeId) : healthyStorage[Math.floor(Math.random() * healthyStorage.length)];
    if (!target || target.status === 'failed') return;

    target.status = 'failed';
    target.failTime = Date.now();
    target.load = 0;

    this.metrics.activeStorageNodes = this.nodes.filter(n => n.type === 'storage' && n.status === 'healthy').length;
    this.metrics.erasureChunksRecovered += 64;

    this.addLog('FAILOVER', 'error', `🚨 STORAGE NODE FAILURE: ${target.name} disk array offline!`);
    this.addLog('FAILOVER', 'sop', `Joltrin Reed-Solomon Erasure Coding activated: Rebuilding missing B-Tree nodes on surviving shards with 0% data loss.`);
    sounds.playAlarm();

    // Trigger auto-healing
    setTimeout(() => {
      if (target.status === 'failed') {
        target.status = 'recovering';
        this.addLog('RECOVERY', 'sop', `Erasure coding reconstruction complete. Resynchronizing WAL to ${target.name}...`);
        this.broadcastState();

        setTimeout(() => {
          target.status = 'healthy';
          target.load = 55;
          this.metrics.activeStorageNodes = this.nodes.filter(n => n.type === 'storage' && n.status === 'healthy').length;
          this.addLog('RECOVERY', 'success', `✓ ${target.name} fully recovered. B-Tree integrity verified 100.00%.`);
          sounds.playRecovery();
          this.broadcastState();
        }, 1500);
      }
    }, 4000);

    this.broadcastState();
  }

  public recoverStorageNode(nodeId?: string) {
    const target = this.nodes.find(n => n.id === (nodeId || 'store-1'));
    if (target && target.status !== 'healthy') {
      target.status = 'healthy';
      this.metrics.activeStorageNodes = this.nodes.filter(n => n.type === 'storage' && n.status === 'healthy').length;
      this.addLog('RECOVERY', 'success', `Manual recovery command completed for ${target.name}.`);
      sounds.playRecovery();
      this.broadcastState();
    }
  }

  public triggerSelfHealing() {
    this.nodes.forEach(n => {
      if (n.status === 'failed' || n.status === 'degraded') {
        n.status = 'recovering';
      }
    });
    this.addLog('RECOVERY', 'sop', 'Joltrin Global Self-Healing Routine initiated across all tiers.');
    sounds.playRecovery();
    this.broadcastState();

    setTimeout(() => {
      this.nodes.forEach(n => {
        n.status = 'healthy';
      });
      this.metrics.activeStorageNodes = this.nodes.filter(n => n.type === 'storage').length;
      this.metrics.activeWorkers = this.nodes.filter(n => n.type === 'worker').length;
      this.metrics.consistencyRate = 100.00;
      this.metrics.reliabilityScore = 99.4;
      this.addLog('RECOVERY', 'success', '✓ All clusters, storage shards, and worker nodes operating at peak consistency.');
      sounds.playVictory();
      this.broadcastState();
    }, 1200);
  }

  public getNodes(): TopologyNode[] {
    return this.nodes;
  }

  public getMetrics(): SystemMetrics {
    return this.metrics;
  }

  public getRecords(): DataRecord[] {
    return this.records;
  }

  public resetSystem() {
    this.resetTopology();
    this.seedRecords();
    this.targetTps = 15000;
    this.metrics = {
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
    };
    this.addLog('SYSTEM', 'info', 'Simulation environment reset to baseline.');
    sounds.playClick();
    this.broadcastState();
  }

  private simulationTick() {
    // Smoothly interpolate current TPS towards target TPS
    const delta = (this.targetTps - this.metrics.tps) * 0.15;
    this.metrics.tps = Math.round(this.metrics.tps + delta);
    this.metrics.totalTransactions += Math.round(this.metrics.tps * 0.25);

    // Update latencies based on load and node health
    const failedStorage = this.nodes.filter(n => n.type === 'storage' && n.status === 'failed').length;
    const failedWorkers = this.nodes.filter(n => n.type === 'worker' && n.status === 'failed').length;

    const baseLat = 0.25 + (this.metrics.tps / 100000) * 0.4 + (failedStorage * 0.35) + (failedWorkers * 0.15);
    this.metrics.avgLatencyMs = Math.round(baseLat * 100) / 100;
    this.metrics.p99LatencyMs = Math.round((baseLat * 2.8) * 100) / 100;

    // Reliability Score calculation
    let score = 100;
    score -= failedStorage * 8;
    score -= failedWorkers * 3;
    if (this.metrics.tps > 80000) score -= 2;
    this.metrics.reliabilityScore = Math.max(72.0, Math.min(100.0, Math.round(score * 10) / 10));

    // Update active workers / tasks dynamically
    const healthyWorkers = this.nodes.filter(n => n.type === 'worker' && n.status === 'healthy');
    if (healthyWorkers.length > 0) {
      const tasksPerWorker = Math.round((this.metrics.tps / 1000) * 8 / healthyWorkers.length);
      healthyWorkers.forEach(w => {
        w.activeTasks = Math.max(10, Math.round(tasksPerWorker + (Math.random() * 8 - 4)));
        w.load = Math.min(95, Math.round((w.activeTasks / w.capacity) * 100));
      });
    }

    // Periodically update some data records
    if (Math.random() > 0.4 && this.records.length > 0) {
      const rec = this.records[Math.floor(Math.random() * this.records.length)];
      rec.version += 1;
      rec.updatedAt = 'Just now';
    }

    this.broadcastState();
  }

  private particleTick() {
    // Spawn particles according to TPS
    const spawnRate = Math.min(6, Math.max(1, Math.floor(this.metrics.tps / 18000)));
    if (Math.random() < 0.6) {
      for (let i = 0; i < spawnRate; i++) {
        this.spawnParticle(Math.random() > 0.5 ? 'tx_write' : 'ai_task');
      }
    }

    // Move existing particles
    const surviving: JobParticle[] = [];
    for (const p of this.particles) {
      p.progress += p.speed;
      if (p.progress < 1.0) {
        surviving.push(p);
      }
    }
    this.particles = surviving;

    if (this.listener) {
      this.listener.onParticlesUpdate(this.particles);
    }
  }

  private spawnParticle(type: 'tx_write' | 'tx_commit' | 'ai_task' | 'rebalance' | 'recovery') {
    const workers = this.nodes.filter(n => n.type === 'worker' && n.status === 'healthy');
    const storage = this.nodes.filter(n => n.type === 'storage' && n.status === 'healthy');

    if (workers.length === 0 || storage.length === 0) return;

    const chosenWorker = workers[Math.floor(Math.random() * workers.length)];
    const chosenStore = storage[Math.floor(Math.random() * storage.length)];

    // Stage 1: Client -> Coordinator
    // Stage 2: Coordinator -> Worker
    // Stage 3: Worker -> Storage
    const stage = Math.random();
    let from = 'node-client';
    let to = 'node-coord';

    if (stage > 0.66) {
      from = 'node-coord';
      to = chosenWorker.id;
    } else if (stage > 0.33) {
      from = chosenWorker.id;
      to = chosenStore.id;
    }

    this.particleIdCounter++;
    this.particles.push({
      id: `p-${this.particleIdCounter}`,
      fromId: from,
      toId: to,
      progress: 0,
      type,
      speed: 0.025 + Math.random() * 0.02
    });
  }

  private addLog(category: LogCategory, level: LogLevel, message: string, detail?: string) {
    this.logIdCounter++;
    const now = new Date();
    const timeStr = now.toTimeString().split(' ')[0] + '.' + String(now.getMilliseconds()).padStart(3, '0');
    const entry: LogEntry = {
      id: `log-${this.instanceId}-${this.logIdCounter}`,
      timestamp: timeStr,
      level,
      category,
      message,
      detail
    };

    if (this.listener) {
      this.listener.onLogEntry(entry);
    }
  }

  private broadcastState() {
    if (!this.listener) return;
    this.listener.onNodesUpdate([...this.nodes]);
    this.listener.onMetricsUpdate({ ...this.metrics });
    this.listener.onRecordsUpdate([...this.records]);
  }
}
