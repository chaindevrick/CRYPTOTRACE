export interface GraphNode {
  data: {
    id: string;
    label: string;
    type: string;
    isTarget?: boolean;
  };
}

export interface GraphEdge {
  data: {
    id: string;
    source: string;
    target: string;
    amount: string;
    time: string;
    edgeLabel: string;
    type: string;
  };
}

export type GraphElement = GraphNode | GraphEdge;

export interface AnalysisStats {
  riskScore: number;
  nodeCount: number;
  mode: 'overview' | 'trace';
}