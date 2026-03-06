'use client';

import React, { useState, useEffect, useRef } from 'react';
import axios from 'axios';
import cytoscape, { Core, NodeSingular, LayoutOptions } from 'cytoscape';
import dagre from 'cytoscape-dagre';
import { Search, Activity, Share2, Target, ShieldAlert, Layers } from 'lucide-react';
import { GraphElement, GraphNode, GraphEdge, AnalysisStats } from '@/types';

// =====================================================================
// Frontend Architecture: Client-Side Layout Engine Initialization
// Design Decision: 將 dagre 佈局引擎的註冊放在模組頂層，並加上 window 檢查。
// Why: Next.js 採用 SSR (Server-Side Rendering)。Cytoscape 是一個純 DOM 依賴的
//      客戶端套件，如果在 Node.js 環境中執行註冊會引發 ReferenceError。
// =====================================================================
if (typeof window !== 'undefined') {
  try {
    cytoscape.use(dagre);
  } catch (e) {
    console.error('Failed to load cytoscape-dagre layout:', e); 
  }
}

export default function ForensicsDashboard() {
  // =====================================================================
  // UI 狀態管理 (React State)
  // Design Decision: 變數命名領域化 (Domain-Specific Naming)
  // =====================================================================
  const [queryIdentifier, setQueryIdentifier] = useState<string>(''); // 替換 targetAddress，因為也可能是 TxHash
  const [isAnalyzing, setIsAnalyzing] = useState<boolean>(false);
  const [hasTopologyData, setHasTopologyData] = useState<boolean>(false);
  const [analysisMode, setAnalysisMode] = useState<'overview' | 'trace'>('overview');
  const [dashboardMetrics, setDashboardMetrics] = useState<AnalysisStats>({ riskScore: 0, nodeCount: 0, mode: 'overview' });
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [liveSyncState, setLiveSyncState] = useState<'syncing' | 'synced'>('synced');

  // =====================================================================
  // DOM 與 WebGL 渲染實例 (Mutable References)
  // Design Decision: 將 Cytoscape 實例綁定在 useRef 而非 useState。
  // Why: Cytoscape 是基於 Canvas/WebGL 的高效能渲染引擎。如果將它放入 React State，
  //      每次圖表更新都會觸發 React 的 Virtual DOM Diffing，導致嚴重的效能瓶頸 (Render Lag)。
  //      使用 useRef 可以完全繞過 React 的渲染週期，直接操作底層圖形。
  // =====================================================================
  const cyRef = useRef<HTMLDivElement>(null);
  const cyInstance = useRef<Core | null>(null);

  // 優先讀取環境變數，實踐 12-Factor App 設定隔離
  const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || 'https://cryptotrace-backend-713204579643.us-central1.run.app';

  // 視覺化回饋：根據動態風險分數決定霓虹光暈顏色
  const getRiskGlowColor = (score: number) => {
    if (score >= 80) return 'text-[#FF003C] drop-shadow-[0_0_12px_rgba(255,0,60,0.6)]';
    if (score >= 50) return 'text-yellow-400';
    return 'text-[#00FF9D] drop-shadow-[0_0_12px_rgba(0,255,157,0.4)]';
  };

  // =====================================================================
  // 核心演算法：動態風險評估引擎 (Dynamic Risk Heuristics)
  // Design Decision: 在客戶端即時運算拓撲風險，降低後端 API 負載。
  // =====================================================================
  const computeRiskMetrics = (graphElements: GraphElement[], currentMode: string) => {
    let computedRisk = 0;
    const uniqueEntities = new Set<string>();

    graphElements.forEach((element) => {
      // 判斷是否為 Node (沒有 source 屬性)
      if (!('source' in element.data)) {
        const node = element as GraphNode;
        uniqueEntities.add(node.data.id);
        
        const isTarget = node.data.isTarget;
        const entityType = node.data.type;

        // 加權計分邏輯
        if (entityType === 'HighRisk' || entityType === 'Mixer') {
          computedRisk += isTarget ? 75 : 15;
        }
      } else {
        const edge = element as GraphEdge;
        if (edge.data.type === 'Trace') computedRisk += 5;
      }
    });

    // 將分數收斂至 0-100 的合理範圍內
    computedRisk = Math.min(100, Math.max(0, computedRisk));
    if (computedRisk === 0) {
      computedRisk = currentMode === 'trace' ? 12 : 5; 
    }

    return { computedRisk, entityCount: uniqueEntities.size };
  };

  // =====================================================================
  // 網路通訊：觸發鑑識分析 (Trigger Forensics Analysis)
  // =====================================================================
  const handleForensicsAnalysis = async () => {
    if (!queryIdentifier) return;
    
    setIsAnalyzing(true);
    setErrorMessage(null);
    setLiveSyncState('syncing'); 
    setHasTopologyData(true); 
    
    if (cyInstance.current) {
      cyInstance.current.destroy();
      cyInstance.current = null;
    }

    try {
      const endpoint = analysisMode === 'trace' ? `${API_BASE_URL}/api/trace` : `${API_BASE_URL}/api/analyze`;
      
      // 非同步等待後端完成完整的 BFS/DFS 擴展與 AI 運算
      await axios.post(endpoint, { address: queryIdentifier });
      
      // 後端處理完畢，切換狀態以終止背景輪詢
      setLiveSyncState('synced'); 
      
      // 獲取 100% 完整的拓撲資料
      const response = await axios.get<GraphElement[]>(`${API_BASE_URL}/api/graph/${queryIdentifier}`);
      const topologyData = response.data;

      if (!topologyData || topologyData.length === 0) {
        setErrorMessage('No actionable data found for this identifier.');
        setHasTopologyData(false);
        return;
      }

      const { computedRisk, entityCount } = computeRiskMetrics(topologyData, analysisMode);

      setDashboardMetrics({
        nodeCount: entityCount, 
        riskScore: computedRisk,   
        mode: analysisMode
      });

      // 稍微延遲渲染，確保 React DOM 已將畫布容器準備就緒
      setTimeout(() => renderTopology(topologyData), 200);

    } catch (error: unknown) { // ✨ 徹底消除 any
      console.error(error);
      // 型別安全 (Type-safe) 的錯誤處理
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

  // =====================================================================
  // 即時資料流 (Live Data Streaming / Long-Polling)
  // Design Decision: 將背景輪詢與 POST 請求解耦 (Decoupled)。
  // Why: 在大型區塊鏈圖譜中，後端可能需要數分鐘才能跑完 AI。透過每 8 秒
  //      拉取一次最新視圖，我們為使用者創造了「資料正在生長」的極佳視覺回饋。
  // =====================================================================
  useEffect(() => {
    let pollingIntervalId: NodeJS.Timeout;

    if (hasTopologyData && queryIdentifier && liveSyncState === 'syncing') {
      pollingIntervalId = setInterval(async () => {
        try {
          const response = await axios.get<GraphElement[]>(`${API_BASE_URL}/api/graph/${queryIdentifier}`);
          const topologyData = response.data;

          if (topologyData && topologyData.length > 0) {
            const { computedRisk, entityCount } = computeRiskMetrics(topologyData, analysisMode);

            setDashboardMetrics(prevMetrics => {
              // 差異比對 (Diffing)：僅在實體數量或風險改變時重新渲染引擎，節省客戶端 CPU
              if (prevMetrics.nodeCount !== entityCount || prevMetrics.riskScore !== computedRisk) {
                setTimeout(() => renderTopology(topologyData), 100);
                return { ...prevMetrics, nodeCount: entityCount, riskScore: computedRisk };
              }
              return prevMetrics;
            });
          }
        } catch (error) {
          console.error('Live sync error:', error);
        }
      }, 8000); 
    }

    // 清理函數 (Cleanup)：防止 Memory Leak 與幽靈 API 請求
    return () => {
      if (pollingIntervalId) clearInterval(pollingIntervalId);
    };
  }, [hasTopologyData, analysisMode, queryIdentifier, liveSyncState]);

  // =====================================================================
  // 視覺化渲染引擎 (Data Visualization Engine)
  // =====================================================================
  const renderTopology = (elements: GraphElement[]) => {
    if (!cyRef.current) return;
    if (cyInstance.current) cyInstance.current.destroy();

    const isTraceMode = analysisMode === 'trace';

    // Design Decision: 動態佈局策略 (Dynamic Layout Strategy)
    // Why:
    //  - Dagre (有向無環圖佈局): 適用於 FLOW 模式，能將洗錢動線從左至右排成一條完美的直線時間軸。
    //  - Concentric (同心圓佈局): 適用於 BROAD 模式，利用我們後端的引力排序，將中心目標放在正中央，
    //    越危險的節點排在越內圈，雜訊節點排在外圈。
    const layoutConfig: LayoutOptions = isTraceMode
      ? {
          name: 'dagre',
          rankDir: 'LR',
          spacingFactor: 1.2,
          animate: true,
          animationDuration: 600,
        } as unknown as LayoutOptions // dagre 是第三方外掛，此處需斷言
      : {
          name: 'concentric',
          fit: true,
          padding: 50,
          minNodeSpacing: 60,
          animate: true,
          animationDuration: 800,
          // ✨ 解決 Node 的 any 報錯，使用嚴格的 cytoscape.NodeSingular 型別
          concentric: (node: NodeSingular) => {
            if (node.data('isTarget')) return 100;
            if (node.data('type') === 'HighRisk' || node.data('type') === 'Mixer') return 80;
            return 10;
          },
          levelWidth: () => 1
        };

    cyInstance.current = cytoscape({
      container: cyRef.current,
      elements: elements,
      minZoom: 0.1,
      maxZoom: 3,
      style: [
        /* ... (保持你原本非常優秀的 stylesheet 設定，此處無需更動) ... */
        {
          selector: 'node',
          style: {
            'background-color': '#1E1E24',
            'border-width': 1.5,
            'border-color': '#444',
            'label': 'data(label)',
            'color': '#888',
            'font-size': '11px',
            'font-family': 'monospace',
            'text-valign': 'bottom',
            'text-margin-y': 8,
            'width': 44,
            'height': 44,
          }
        },
        {
          selector: 'node[?isTarget]',
          style: {
            'background-color': '#000',
            'border-color': '#00E0FF',
            'border-width': 3,
            'width': 64,
            'height': 64,
            'underlay-color': '#00E0FF',
            'underlay-padding': 15,
            'underlay-opacity': 0.5,
            'underlay-shape': 'ellipse',
            'color': '#FFF'
          }
        },
        {
          selector: 'node[type="Mixer"], node[type="risk"]',
          style: {
            'background-color': '#1A0505',
            'border-color': '#FF003C',
            'shape': 'diamond',
            'width': 54,
            'height': 54,
            'underlay-color': '#FF003C',
            'underlay-padding': 12,
            'underlay-opacity': 0.5,
            'underlay-shape': 'round-rectangle',
          }
        },
        {
          selector: 'node[type="HighRisk"]',
          style: {
            'background-color': '#3a0000',
            'border-color': '#FF3366',
            'border-width': 2,
            'underlay-color': '#FF003C',
            'underlay-padding': 10,
            'underlay-opacity': 0.6,
          }
        },
        {
          selector: 'edge',
          style: {
            'width': 1.5,
            'line-color': '#333',
            'target-arrow-color': '#333',
            'target-arrow-shape': 'triangle',
            'curve-style': 'bezier',
            'label': 'data(edgeLabel)', 
            'text-wrap': 'wrap',
            'text-margin-y': -12,
            'text-halign': 'center',
            'text-valign': 'top',
            'color': '#888',
            'font-size': '9px',
            'font-family': 'monospace',
            'text-background-opacity': 1,
            'text-background-color': '#0A0A0A',
            'text-background-padding': '4px',
            'text-background-shape': 'roundrectangle',
            'control-point-step-size': 40 
          }
        },
        {
          selector: 'edge[type="Trace"]',
          style: {
            'line-color': '#FF003C',
            'target-arrow-color': '#FF003C',
            'width': 2.5,
            'color': '#FF003C',
          }
        }
      ],
      layout: layoutConfig
    });

    // 互動設計：允許點擊畫布上的節點進行深度下鑽 (Drill-down)
    cyInstance.current.on('tap', 'node', function(evt) {
      const node = evt.target;
      setQueryIdentifier(node.id());
    });
  };

  const centerTopologyView = () => cyInstance.current?.fit(cyInstance.current.elements(), 50);

  // 響應式設計：監聽視窗大小改變並重新計算佈局
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
          <div className="flex items-center gap-3 mb-8">
            <ShieldAlert className="text-[#00E0FF]" size={28} />
            <h1 className="text-xl font-bold tracking-[0.2em] text-white">
              CRYPTO<span className="text-[#00E0FF]">TRACE</span>
            </h1>
          </div>

          <div className="relative mb-6 group">
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
            <div className="mt-6 p-3 bg-red-950/40 border border-red-500/30 rounded-lg text-red-400 text-xs font-mono text-center">
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
            <span className="w-2.5 h-2.5 rounded-full bg-[#00E0FF] shadow-[0_0_8px_#00E0FF]"></span> Subject (Target)
          </div>
          <div className="flex items-center gap-3 mb-3 text-xs font-mono text-slate-300">
            <span className="w-2.5 h-2.5 rounded-full bg-[#3a0000] border border-[#FF3366] shadow-[0_0_8px_rgba(255,0,60,0.5)]"></span> AI High Risk
          </div>
          <div className="flex items-center gap-3 text-xs font-mono text-slate-300">
            <span className="w-2.5 h-2.5 rounded-full bg-[#444] border border-slate-600"></span> Standard Node
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