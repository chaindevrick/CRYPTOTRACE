'use client';

import React, { useState, useEffect, useRef } from 'react';
import axios from 'axios';
import cytoscape, { Core } from 'cytoscape';
import dagre from 'cytoscape-dagre';
import { Search, Activity, Share2, Target, ShieldAlert, Layers } from 'lucide-react';
import { GraphElement, GraphNode, GraphEdge, AnalysisStats } from '@/types';

if (typeof window !== 'undefined') {
  try {
    cytoscape.use(dagre);
  } catch (e) {
    console.error('Failed to load cytoscape-dagre layout:', e); 
  }
}

export default function ForensicsDashboard() {
  const [targetAddress, setTargetAddress] = useState<string>('');
  const [analyzing, setAnalyzing] = useState<boolean>(false);
  const [hasGraphData, setHasGraphData] = useState<boolean>(false);
  const [mode, setMode] = useState<'overview' | 'trace'>('overview');
  const [stats, setStats] = useState<AnalysisStats>({ riskScore: 0, nodeCount: 0, mode: 'overview' });
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const cyRef = useRef<HTMLDivElement>(null);
  const cyInstance = useRef<Core | null>(null);

  const getRiskColor = (score: number) => {
    if (score >= 80) return 'text-[#FF003C] drop-shadow-[0_0_12px_rgba(255,0,60,0.6)]';
    if (score >= 50) return 'text-yellow-400';
    return 'text-[#00FF9D] drop-shadow-[0_0_12px_rgba(0,255,157,0.4)]';
  };

  const handleAnalysis = async () => {
    if (!targetAddress) return;
    
    setAnalyzing(true);
    setHasGraphData(false);
    setErrorMsg(null);
    
    if (cyInstance.current) {
      cyInstance.current.destroy();
      cyInstance.current = null;
    }

    try {
      const endpoint = mode === 'trace' ? '/api/trace' : '/api/analyze';
      await axios.post(endpoint, { address: targetAddress });
      
      const response = await axios.get<GraphElement[]>(`/api/graph/${targetAddress}`);
      const graphData = response.data;

      if (!graphData || graphData.length === 0) {
        setErrorMsg('No actionable data found for this address.');
        return;
      }

      // ✨ 核心升級：動態風險評估引擎 (Dynamic Risk Scoring) 且具備嚴格型別安全
      let calculatedRisk = 0;
      const uniqueNodes = new Set();

      graphData.forEach((element) => {
        // ✨ TypeScript 類型保護 (Type Guard)：用 'source' 來分辨 Node 還是 Edge
        if (!('source' in element.data)) {
          // 這是一顆 Node (安全轉型)
          const node = element as GraphNode;
          uniqueNodes.add(node.data.id);
          
          const isTarget = node.data.isTarget;
          const nodeType = node.data.type;

          // 規則 A：如果有關聯的節點是高風險或混幣器
          if (nodeType === 'HighRisk' || nodeType === 'Mixer') {
            if (isTarget) {
              calculatedRisk += 75; // 目標本身是危險的，直接 +75 分
            } else {
              calculatedRisk += 15; // 關聯節點是危險的，每個 +15 分
            }
          }
        } else {
          // 這是一條 Edge (安全轉型)
          const edge = element as GraphEdge;
          // 規則 B：如果是被 Trace 演算法抓出來的精準連線
          if (edge.data.type === 'Trace') {
            calculatedRisk += 5; // 每多一層洗錢轉移，+5 分
          }
        }
      });

      // 規則 C：確保分數在 0 到 100 之間
      calculatedRisk = Math.min(100, Math.max(0, calculatedRisk));
      
      // 給予基礎基準分
      if (calculatedRisk === 0) {
        calculatedRisk = mode === 'trace' ? 12 : 5; 
      }

      setStats({
        nodeCount: uniqueNodes.size, // 精準計算節點數量 (不包含連線)
        riskScore: calculatedRisk,   // 帶入動態計算的風險分數
        mode: mode
      });

      setHasGraphData(true);
      setTimeout(() => renderGraph(graphData), 200);

    } catch (err: any) {
      console.error(err);
      setErrorMsg(err.response?.data?.error || 'Analysis engine failure.');
    } finally {
      setAnalyzing(false);
    }
  };

  const renderGraph = (elements: GraphElement[]) => {
    if (!cyRef.current) return;
    if (cyInstance.current) cyInstance.current.destroy();

    const isTrace = mode === 'trace';

    cyInstance.current = cytoscape({
      container: cyRef.current,
      elements: elements,
      minZoom: 0.1,
      maxZoom: 3,
      style: [
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
          // 目標節點 (使用 isTarget 判斷)
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
          // 實名已知交易所 / 混幣器
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
          // AI 標記的高風險錢包
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
      layout: (isTrace 
        ? {
            name: 'dagre',
            rankDir: 'LR',
            spacingFactor: 1.2,
            animate: true,
            animationDuration: 600,
          }
        : {
            name: 'concentric',
            fit: true,
            padding: 50,
            minNodeSpacing: 60,
            animate: true,
            animationDuration: 800,
            concentric: (node: any) => {
              if (node.data('isTarget')) return 100; // 目標在最內圈
              if (node.data('type') === 'HighRisk' || node.data('type') === 'Mixer') return 80;
              return 10;
            },
            levelWidth: () => 1
          }) as any
    });

    // ✨ 點擊事件：點擊任意節點，自動將完整地址填入搜尋框
    cyInstance.current.on('tap', 'node', function(evt) {
      const node = evt.target;
      setTargetAddress(node.id());
    });
  };

  const centerGraph = () => cyInstance.current?.fit(cyInstance.current.elements(), 50);

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
              placeholder="點擊畫布上的節點或輸入地址..."
              value={targetAddress}
              onChange={(e) => setTargetAddress(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleAnalysis()}
              spellCheck={false}
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <button
              onClick={() => { setMode('overview'); if(targetAddress) handleAnalysis(); }}
              disabled={analyzing}
              className={`flex items-center justify-center gap-2 py-3 rounded-lg text-xs font-semibold tracking-wider transition-all border ${
                mode === 'overview' 
                  ? 'bg-[#00E0FF]/10 text-[#00E0FF] border-[#00E0FF]/40 shadow-[0_0_15px_rgba(0,224,255,0.15)]' 
                  : 'bg-white/5 text-slate-400 border-transparent hover:bg-white/10'
              }`}
            >
              <Activity size={16} /> BROAD
            </button>
            <button
              onClick={() => { setMode('trace'); if(targetAddress) handleAnalysis(); }}
              disabled={analyzing}
              className={`flex items-center justify-center gap-2 py-3 rounded-lg text-xs font-semibold tracking-wider transition-all border ${
                mode === 'trace' 
                  ? 'bg-[#FF003C]/10 text-[#FF003C] border-[#FF003C]/40 shadow-[0_0_15px_rgba(255,0,60,0.15)]' 
                  : 'bg-white/5 text-slate-400 border-transparent hover:bg-white/10'
              }`}
            >
              <Share2 size={16} /> FLOW
            </button>
          </div>

          {errorMsg && (
            <div className="mt-6 p-3 bg-red-950/40 border border-red-500/30 rounded-lg text-red-400 text-xs font-mono text-center">
              {errorMsg}
            </div>
          )}
        </div>
      </div>

      {hasGraphData && (
        <div className="absolute top-8 right-8 z-20 w-[280px]">
          <div className="bg-[#121216]/80 backdrop-blur-xl border border-white/10 rounded-xl overflow-hidden shadow-2xl">
            <div className="bg-white/5 px-5 py-3 border-b border-white/5 flex items-center justify-between">
              <span className="text-[10px] tracking-[0.15em] font-bold text-slate-400 uppercase">Intelligence</span>
              <div className="flex h-2 w-2 relative">
                <span className={`animate-ping absolute inline-flex h-full w-full rounded-full opacity-75 ${stats.mode === 'trace' ? 'bg-[#FF003C]' : 'bg-[#00E0FF]'}`}></span>
                <span className={`relative inline-flex rounded-full h-2 w-2 ${stats.mode === 'trace' ? 'bg-[#FF003C]' : 'bg-[#00E0FF]'}`}></span>
              </div>
            </div>
            
            <div className="p-8 text-center border-b border-white/5">
              <div className={`text-6xl font-bold font-mono tracking-tighter ${getRiskColor(stats.riskScore)}`}>
                {stats.riskScore}
              </div>
              <div className="text-[10px] tracking-widest text-slate-500 mt-3 uppercase">Computed Risk Score</div>
            </div>

            <div className="grid grid-cols-2 divide-x divide-white/5">
              <div className="p-5 flex flex-col items-center">
                <span className="text-[10px] tracking-widest text-slate-500 uppercase mb-2">Entities</span>
                <span className="font-mono text-lg font-medium text-white">{stats.nodeCount}</span>
              </div>
              <div className="p-5 flex flex-col items-center">
                <span className="text-[10px] tracking-widest text-slate-500 uppercase mb-2">Vector</span>
                <span className={`font-mono text-sm font-bold mt-1 ${stats.mode === 'overview' ? 'text-[#00E0FF]' : 'text-[#FF003C]'}`}>
                  {stats.mode === 'overview' ? 'N-DEGREE' : 'LINEAR'}
                </span>
              </div>
            </div>
          </div>
        </div>
      )}

      <div className="absolute bottom-8 right-8 z-20 flex flex-col gap-4 items-end">
        <button onClick={centerGraph} className="p-3 bg-[#121216]/80 backdrop-blur-xl border border-white/10 rounded-xl hover:bg-white/10 transition-colors text-slate-300 hover:text-white" title="Recenter Topology">
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

      {analyzing && (
        <div className="absolute inset-0 z-50 bg-[#0A0A0C]/90 backdrop-blur-md flex flex-col items-center justify-center">
          <div className="relative w-64 h-1 bg-[#1E1E24] rounded-full overflow-hidden mb-6">
            <div className="absolute top-0 bottom-0 left-0 bg-[#00E0FF] shadow-[0_0_15px_#00E0FF] w-1/2 animate-[scan_1s_ease-in-out_infinite_alternate]" />
          </div>
          <div className="font-mono text-[#00E0FF] tracking-[0.2em] text-sm animate-pulse">
            {mode === 'trace' ? 'TRACING ILLICIT FLOWS...' : 'SCANNING LEDGER...'}
          </div>
        </div>
      )}

      {!hasGraphData && !analyzing && (
        <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 flex flex-col items-center pointer-events-none opacity-20">
          <Layers size={64} className="mb-6 text-slate-400" />
          <div className="font-mono tracking-[0.4em] text-sm font-bold text-slate-400">SYSTEM IDLE</div>
        </div>
      )}
    </main>
  );
}