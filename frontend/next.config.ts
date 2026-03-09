import type { NextConfig } from "next";

// 偵測是否指定了靜態匯出模式
const isExportMode = process.env.NEXT_OUTPUT_MODE === 'export';

const nextConfig: NextConfig = {
  // 核心架構切換：根據部署目標決定 Output 格式
  output: isExportMode ? 'export' : 'standalone',
  
  images: {
    // 靜態匯出模式不支援 Next.js 的預設圖片最佳化伺服器
    unoptimized: isExportMode,
  },

  // (選填) 防禦性設計：如果你有關閉 React 嚴格模式的需求，可以在這裡設定
  // reactStrictMode: true, 
};

export default nextConfig;