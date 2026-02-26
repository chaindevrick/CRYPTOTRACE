import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // 讓 Docker 打包出極小體積的獨立執行檔
  output: 'standalone',
  
  // 內建反向代理：取代 Nginx 的 proxy_pass
  async rewrites() {
    return [
      {
        source: '/api/:path*',
        // 這裡的 backend 是 docker-compose 裡的 Go 服務名稱
        destination: 'http://backend:3000/api/:path*', 
      },
    ];
  },
};

export default nextConfig;
