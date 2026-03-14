'use client';

import React, { useState, useEffect, useRef, useCallback } from 'react';
import axios from 'axios';
import cytoscape, { Core, NodeSingular, LayoutOptions } from 'cytoscape';
import dagre from 'cytoscape-dagre';
import { Search, Activity, Share2, Target, ShieldAlert, Layers, Calendar } from 'lucide-react';
import { GraphElement, GraphNode, GraphEdge, AnalysisStats } from '@/types';

if (typeof window !== 'undefined') {
  try { cytoscape.use(dagre); } catch (e) {}
}

// =====================================================================
// 🎨 色彩產生器 (Hash-to-Color)
// Design Decision: 將字串雜湊為 HSL 色碼。
// Why: 確保同一個地址永遠配對同一個顏色，且 S=85%, L=65% 確保在深色模式下非常亮眼。
// =====================================================================
const stringToColor = (str: string) => {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    hash = str.charCodeAt(i) + ((hash << 5) - hash);
  }
  const h = Math.abs(hash) % 360;
  return `hsl(${h}, 85%, 65%)`;
};

export default function ForensicsDashboard() {
  const [queryIdentifier, setQueryIdentifier] = useState<string>('');
  const [startDate, setStartDate] = useState<string>('');
  const [endDate, setEndDate] = useState<string>('');
  
  const [isAnalyzing, setIsAnalyzing] = useState<boolean>(false);
  const [hasTopologyData, setHasTopologyData] = useState<boolean>(false);
  const [analysisMode, setAnalysisMode] = useState<'overview' | 'trace'>('overview');
  const [dashboardMetrics, setDashboardMetrics] = useState<AnalysisStats>({ riskScore: 0, nodeCount: 0, mode: 'overview' });
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [liveSyncState, setLiveSyncState] = useState<'syncing' | 'synced'>('synced');

  const cyRef = useRef<HTMLDivElement>(null);
  const cyInstance = useRef<Core | null>(null);
  const unchangedCountRef = useRef<number>(0);

  const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || 'https://cryptotrace-backend-713204579643.us-central1.run.app';

  const getUnixTimestamp = useCallback((dateString: string, isEnd: boolean = false) => {
    if (!dateString) return 0;
    const date = new Date(dateString);
    if (isEnd) date.setHours(23, 59, 59, 999);
    return Math.floor(date.getTime() / 1000);
  }, []);

  const getRiskGlowColor = (score: number) => {
    if (score >= 80) return 'text-[#FF003C] drop-shadow-[0_0_12px_rgba(255,0,60,0.6)]';
    if (score >= 50) return 'text-yellow-400';
    return 'text-[#00FF9D] drop-shadow-[0_0_12px_rgba(0,255,157,0.4)]';
  };

  const computeRiskMetrics = (graphElements: GraphElement[], currentMode: string) => {
    let computedRisk = 0;
    const uniqueEntities = new Set<string>();

    graphElements.forEach((element) => {
      if (!('source' in element.data)) {
        const node = element as GraphNode;
        uniqueEntities.add(node.data.id);
        const isTarget = node.data.isTarget;
        const entityType = node.data.type;
        if (entityType === 'HighRisk' || entityType === 'Mixer') {
          computedRisk += isTarget ? 75 : 15;
        }
      } else {
        const edge = element as GraphEdge;
        if (edge.data.type === 'Trace') computedRisk += 5;
      }
    });

    computedRisk = Math.min(100, Math.max(0, computedRisk));
    if (computedRisk === 0) computedRisk = currentMode === 'trace' ? 12 : 5; 
    return { computedRisk, entityCount: uniqueEntities.size };
  };

const renderTopology = useCallback((elements: GraphElement[]) => {
    if (!cyRef.current) return;
    if (cyInstance.current) cyInstance.current.destroy();

    const styledElements = elements.map(el => {
      const newEl = JSON.parse(JSON.stringify(el));
      if (!newEl.data.source) {
        newEl.data.color = stringToColor(newEl.data.id);
      } else {
        newEl.data.color = stringToColor(newEl.data.source);
      }
      return newEl;
    });

    // =====================================================================
    // 📐 佈局引擎升級：全面採用 Dagre (有向無環圖) 階層佈局
    // Design Decision: 將資金流向強制約束為「從左到右 (Left-to-Right)」的樹狀/網狀結構。
    // Why: 完美呈現資金的流動方向。最左邊通常是資金源頭 (Source)，
    //      隨著層數向右推進，可以清晰看見資金被「切碎分發 (剝皮鏈)」或是「匯聚 (混幣器)」。
    // =====================================================================
    const layoutConfig = {
      name: 'dagre',
      rankDir: 'LR',        // 核心設定：LR 代表 Left to Right (由左至右排列)
      spacingFactor: 1.2,   // 節點整體的擴張倍率
      nodeSep: 50,          // 垂直方向：同一層（上下）節點的間距
      rankSep: 150,         // 水平方向：層與層（左右）之間的間距，拉開一點線條會更清楚
      animate: true,
      animationDuration: 800,
    } as unknown as LayoutOptions;

    cyInstance.current = cytoscape({
      container: cyRef.current,
      elements: styledElements,
      minZoom: 0.1, maxZoom: 3,
      style: [
        {
          selector: 'node',
          style: {
            'background-color': 'data(color)', 
            'border-width': 2, 
            'border-color': '#111', 
            'label': 'data(label)',
            'color': '#CCC', 
            'font-size': '11px', 'font-family': 'monospace', 'text-valign': 'bottom',
            'text-margin-y': 8, 'width': 44, 'height': 44,
          }
        },
        {
          selector: 'node[?isTarget]',
          style: {
            'border-color': '#FFF', 
            'border-width': 4, 'width': 64, 'height': 64,
            'underlay-color': '#00E0FF', 'underlay-padding': 15, 'underlay-opacity': 0.8, 'underlay-shape': 'ellipse', 'color': '#FFF'
          }
        },
        {
          selector: 'node[type="Mixer"], node[type="risk"]',
          style: {
            'shape': 'diamond', 'width': 54, 'height': 54,
          }
        },
        {
          selector: 'node[type="HighRisk"]',
          style: {
            'background-opacity': 0.6,
            'border-color': '#FF003C', 
            'border-width': 4,
            'underlay-color': '#FF003C', 'underlay-padding': 12, 'underlay-opacity': 0.9,
          }
        },
        {
          selector: 'edge',
          style: {
            'width': 2.5, 
            'line-color': 'data(color)', 
            'target-arrow-color': 'data(color)', 
            'target-arrow-shape': 'triangle',
            // 💡 配合 LR 佈局，建議將線條改為 taxi 或 bezier，這裡使用 bezier 會有漂亮的弧線
            'curve-style': 'bezier', 
            'label': 'data(edgeLabel)', 'text-wrap': 'wrap', 'text-margin-y': -12,
            'text-halign': 'center', 'text-valign': 'top', 'color': '#888', 'font-size': '9px', 'font-family': 'monospace',
            'text-background-opacity': 1, 'text-background-color': '#0A0A0A', 'text-background-padding': '4px',
            'text-background-shape': 'roundrectangle', 'control-point-step-size': 40,
            'opacity': 0.85 
          }
        },
        {
          selector: 'edge[type="Trace"]',
          style: { 'line-color': '#FF003C', 'target-arrow-color': '#FF003C', 'width': 3.5, 'opacity': 1 }
        }
      ],
      layout: layoutConfig
    });

    cyInstance.current.on('tap', 'node, edge', function(evt) {
      setQueryIdentifier(evt.target.id());
    });
  }, []);

  const handleForensicsAnalysis = async () => {
    if (!queryIdentifier) return;
    
    setIsAnalyzing(true);
    setErrorMessage(null);
    setLiveSyncState('syncing'); 
    setHasTopologyData(true); 
    unchangedCountRef.current = 0;
    
    if (cyInstance.current) {
      cyInstance.current.destroy();
      cyInstance.current = null;
    }

    try {
      const endpoint = analysisMode === 'trace' ? `${API_BASE_URL}/api/trace` : `${API_BASE_URL}/api/analyze`;
      
      const startTs = getUnixTimestamp(startDate);
      const endTs = getUnixTimestamp(endDate, true);

      await axios.post(endpoint, { 
        address: queryIdentifier,
        startTime: startTs,
        endTime: endTs
      });
      
      const getGraphUrl = `${API_BASE_URL}/api/graph/${queryIdentifier}?start=${startTs}&end=${endTs}`;
      const response = await axios.get<GraphElement[]>(getGraphUrl);
      const topologyData = response.data;

      if (!topologyData || topologyData.length === 0) {
        setErrorMessage('No actionable data found in this time window.');
        setHasTopologyData(false);
        setLiveSyncState('synced');
        return;
      }

      const { computedRisk, entityCount } = computeRiskMetrics(topologyData, analysisMode);
      setDashboardMetrics({ nodeCount: entityCount, riskScore: computedRisk, mode: analysisMode });
      setTimeout(() => renderTopology(topologyData), 200);

    } catch (error: unknown) {
      if (axios.isAxiosError(error)) {
        setErrorMessage(error.response?.data?.error || 'Analysis engine failure.');
      } else {
        setErrorMessage('An unexpected internal error occurred.');
      }
      setLiveSyncState('synced'); 
      setHasTopologyData(false);
    } finally {
      setIsAnalyzing(false);
    }
  };

  useEffect(() => {
    let pollingIntervalId: NodeJS.Timeout;

    const fetchLatestTopology = async () => {
      try {
        const startTs = getUnixTimestamp(startDate);
        const endTs = getUnixTimestamp(endDate, true);
        const getGraphUrl = `${API_BASE_URL}/api/graph/${queryIdentifier}?start=${startTs}&end=${endTs}`;
        
        const response = await axios.get<GraphElement[]>(getGraphUrl);
        const topologyData = response.data;

        if (topologyData && topologyData.length > 0) {
          const { computedRisk, entityCount } = computeRiskMetrics(topologyData, analysisMode);

          setDashboardMetrics(prevMetrics => {
            if (prevMetrics.nodeCount !== entityCount || prevMetrics.riskScore !== computedRisk) {
              unchangedCountRef.current = 0; 
              setTimeout(() => renderTopology(topologyData), 100);
              return { ...prevMetrics, nodeCount: entityCount, riskScore: computedRisk };
            } else {
              unchangedCountRef.current += 1;
              if (unchangedCountRef.current >= 15) setLiveSyncState('synced');
              return prevMetrics;
            }
          });
        }
      } catch (error) {
        console.error('Live sync error:', error);
      }
    };

    if (hasTopologyData && queryIdentifier && liveSyncState === 'syncing') {
      pollingIntervalId = setInterval(fetchLatestTopology, 8000); 
    }

    return () => {
      if (pollingIntervalId) clearInterval(pollingIntervalId);
    };
  }, [hasTopologyData, analysisMode, queryIdentifier, liveSyncState, API_BASE_URL, renderTopology, startDate, endDate, getUnixTimestamp]);

  const centerTopologyView = () => cyInstance.current?.fit(cyInstance.current.elements(), 50);

  useEffect(() => {
    const handleResize = () => cyInstance.current?.resize();
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  return (
    <main className="relative w-screen h-screen bg-[#0A0A0C] text-slate-200 overflow-hidden font-sans selection:bg-[#00E0FF] selection:text-black">
      <div className="absolute inset-0 z-0 bg-[radial-gradient(#ffffff15_1px,transparent_1px)] [background-size:24px_24px] pointer-events-none" />
      <div ref={cyRef} style={{ position: 'absolute', top: 0, left: 0, width: '100vw', height: '100vh', zIndex: 10 }} />

      <div className="absolute top-8 left-8 z-20 w-[400px] flex flex-col gap-6">
        <div className="bg-[#121216]/80 backdrop-blur-xl border border-white/10 rounded-xl p-6 shadow-2xl">
          <div className="flex items-center gap-3 mb-6">
            <ShieldAlert className="text-[#00E0FF]" size={28} />
            <h1 className="text-xl font-bold tracking-[0.2em] text-white">
              CRYPTO<span className="text-[#00E0FF]">TRACE</span>
            </h1>
          </div>

          <div className="relative mb-4 group">
            <Search className="absolute left-4 top-1/2 -translate-y-1/2 text-slate-500 group-focus-within:text-[#00E0FF] transition-colors" size={18} />
            <input
              type="text"
              className="w-full bg-black/50 border border-white/10 rounded-lg py-3.5 pl-11 pr-4 text-sm font-mono focus:outline-none focus:border-[#00E0FF]/50 focus:ring-1 focus:ring-[#00E0FF]/50 transition-all placeholder:text-slate-600"
              placeholder="輸入錢包地址或交易哈希..."
              value={queryIdentifier}
              onChange={(e) => setQueryIdentifier(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleForensicsAnalysis()}
              spellCheck={false}
            />
          </div>

          <div className="flex gap-3 mb-6">
            <div className="flex-1 relative group">
              <div className="flex items-center gap-1.5 text-[10px] text-slate-500 mb-1 ml-1 uppercase tracking-widest font-bold">
                <Calendar size={10} className="text-[#00E0FF]" /> Start Date
              </div>
              <input
                type="date"
                style={{ colorScheme: 'dark' }}
                className="w-full bg-black/50 border border-white/10 rounded-lg py-2.5 px-3 text-xs font-mono text-slate-300 focus:outline-none focus:border-[#00E0FF]/50 focus:ring-1 focus:ring-[#00E0FF]/50 transition-all cursor-pointer"
                value={startDate}
                onChange={(e) => setStartDate(e.target.value)}
              />
            </div>
            <div className="flex-1 relative group">
              <div className="flex items-center gap-1.5 text-[10px] text-slate-500 mb-1 ml-1 uppercase tracking-widest font-bold">
                <Calendar size={10} className="text-[#00E0FF]" /> End Date
              </div>
              <input
                type="date"
                style={{ colorScheme: 'dark' }}
                className="w-full bg-black/50 border border-white/10 rounded-lg py-2.5 px-3 text-xs font-mono text-slate-300 focus:outline-none focus:border-[#00E0FF]/50 focus:ring-1 focus:ring-[#00E0FF]/50 transition-all cursor-pointer"
                value={endDate}
                onChange={(e) => setEndDate(e.target.value)}
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <button
              onClick={() => { setAnalysisMode('overview'); if(queryIdentifier) handleForensicsAnalysis(); }}
              disabled={isAnalyzing}
              className={`flex items-center justify-center gap-2 py-3 rounded-lg text-xs font-semibold tracking-wider transition-all border ${
                analysisMode === 'overview' 
                  ? 'bg-[#00E0FF]/10 text-[#00E0FF] border-[#00E0FF]/40 shadow-[0_0_15px_rgba(0,224,255,0.15)]' 
                  : 'bg-white/5 text-slate-400 border-transparent hover:bg-white/10'
              }`}
            >
              <Activity size={16} /> BROAD
            </button>
            <button
              onClick={() => { setAnalysisMode('trace'); if(queryIdentifier) handleForensicsAnalysis(); }}
              disabled={isAnalyzing}
              className={`flex items-center justify-center gap-2 py-3 rounded-lg text-xs font-semibold tracking-wider transition-all border ${
                analysisMode === 'trace' 
                  ? 'bg-[#FF003C]/10 text-[#FF003C] border-[#FF003C]/40 shadow-[0_0_15px_rgba(255,0,60,0.15)]' 
                  : 'bg-white/5 text-slate-400 border-transparent hover:bg-white/10'
              }`}
            >
              <Share2 size={16} /> FLOW
            </button>
          </div>

          {errorMessage && (
            <div className="mt-4 p-3 bg-red-950/40 border border-red-500/30 rounded-lg text-red-400 text-xs font-mono text-center">
              {errorMessage}
            </div>
          )}
        </div>
      </div>

      {hasTopologyData && (
        <div className="absolute top-8 right-8 z-20 w-[280px]">
          <div className="bg-[#121216]/80 backdrop-blur-xl border border-white/10 rounded-xl overflow-hidden shadow-2xl">
            <div className="bg-white/5 px-5 py-3 border-b border-white/5 flex items-center justify-between">
              <span className="text-[10px] tracking-[0.15em] font-bold text-slate-400 uppercase flex items-center gap-2">
                Intelligence
                {liveSyncState === 'syncing' && (
                  <span className="text-[#00E0FF] tracking-widest text-[8px] animate-pulse">(LIVE SYNC)</span>
                )}
                {liveSyncState === 'synced' && (
                  <span className="text-[#00FF9D] tracking-widest text-[8px]">(SYNCED)</span>
                )}
              </span>
              <div className="flex h-2 w-2 relative">
                <span className={`animate-ping absolute inline-flex h-full w-full rounded-full opacity-75 ${dashboardMetrics.mode === 'trace' ? 'bg-[#FF003C]' : 'bg-[#00E0FF]'}`}></span>
                <span className={`relative inline-flex rounded-full h-2 w-2 ${dashboardMetrics.mode === 'trace' ? 'bg-[#FF003C]' : 'bg-[#00E0FF]'}`}></span>
              </div>
            </div>
            
            <div className="p-8 text-center border-b border-white/5">
              <div className={`text-6xl font-bold font-mono tracking-tighter transition-colors duration-1000 ${getRiskGlowColor(dashboardMetrics.riskScore)}`}>
                {dashboardMetrics.riskScore}
              </div>
              <div className="text-[10px] tracking-widest text-slate-500 mt-3 uppercase">Computed Risk Score</div>
            </div>

            <div className="grid grid-cols-2 divide-x divide-white/5">
              <div className="p-5 flex flex-col items-center">
                <span className="text-[10px] tracking-widest text-slate-500 uppercase mb-2">Entities</span>
                <span className="font-mono text-lg font-medium text-white transition-all">{dashboardMetrics.nodeCount}</span>
              </div>
              <div className="p-5 flex flex-col items-center">
                <span className="text-[10px] tracking-widest text-slate-500 uppercase mb-2">Vector</span>
                <span className={`font-mono text-sm font-bold mt-1 ${dashboardMetrics.mode === 'overview' ? 'text-[#00E0FF]' : 'text-[#FF003C]'}`}>
                  {dashboardMetrics.mode === 'overview' ? 'N-DEGREE' : 'LINEAR'}
                </span>
              </div>
            </div>
          </div>
        </div>
      )}

      <div className="absolute bottom-8 right-8 z-20 flex flex-col gap-4 items-end">
        <button onClick={centerTopologyView} className="p-3 bg-[#121216]/80 backdrop-blur-xl border border-white/10 rounded-xl hover:bg-white/10 transition-colors text-slate-300 hover:text-white" title="Recenter Topology">
          <Target size={20} />
        </button>

        <div className="bg-[#121216]/80 backdrop-blur-xl border border-white/10 rounded-xl p-5 min-w-[160px]">
          <div className="text-[10px] tracking-widest font-bold text-slate-500 uppercase mb-4">Topology Key</div>
          <div className="flex items-center gap-3 mb-3 text-xs font-mono text-slate-300">
            <span className="w-3 h-3 rounded-full border border-slate-600" style={{ background: 'linear-gradient(135deg, hsl(180, 85%, 65%), hsl(220, 85%, 65%))'}}></span> Entity Color
          </div>
          <div className="flex items-center gap-3 mb-3 text-xs font-mono text-slate-300">
            <span className="w-3 h-3 rounded-full bg-[#00E0FF] border-2 border-white shadow-[0_0_8px_#00E0FF]"></span> Target Subject
          </div>
          <div className="flex items-center gap-3 text-xs font-mono text-slate-300">
            <span className="w-3 h-3 rounded-full bg-slate-500/60 border-2 border-[#FF003C] shadow-[0_0_8px_rgba(255,0,60,0.8)]"></span> AI High Risk
          </div>
        </div>
      </div>

      {isAnalyzing && (
        <div className="absolute inset-0 z-50 bg-[#0A0A0C]/70 backdrop-blur-sm flex flex-col items-center justify-center pointer-events-none">
          <div className="relative w-64 h-1 bg-[#1E1E24] rounded-full overflow-hidden mb-6">
            <div className="absolute top-0 bottom-0 left-0 bg-[#00E0FF] shadow-[0_0_15px_#00E0FF] w-1/2 animate-[scan_1s_ease-in-out_infinite_alternate]" />
          </div>
          <div className="font-mono text-[#00E0FF] tracking-[0.2em] text-sm animate-pulse">
            {analysisMode === 'trace' ? 'TRACING ILLICIT FLOWS...' : 'SCANNING LEDGER...'}
          </div>
        </div>
      )}

      {!hasTopologyData && !isAnalyzing && (
        <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 flex flex-col items-center pointer-events-none opacity-20">
          <Layers size={64} className="mb-6 text-slate-400" />
          <div className="font-mono tracking-[0.4em] text-sm font-bold text-slate-400">SYSTEM IDLE</div>
        </div>
      )}
    </main>
  );
}