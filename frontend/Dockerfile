# =====================================================================
# Stage 1: Dependencies & Builder
# Design Decision: 多階段構建 (Multi-stage Build)。
# Why: 在這個階段我們會安裝所有的 devDependencies 並進行編譯。
#      編譯完成後，這些笨重的編譯工具都不會進入最終的 Image 中，大幅縮減映像檔體積。
# =====================================================================
FROM node:20-alpine AS builder
WORKDIR /app

# 利用 Docker 的層級快取 (Layer Caching) 加速 npm install
COPY package.json package-lock.json ./
RUN npm ci

COPY . .
# 這裡不需要設定 NEXT_OUTPUT_MODE，讓 next.config.ts 自動判斷為 'standalone'
RUN npm run build

# =====================================================================
# Stage 2: Production Runner
# Design Decision: 最小化運行環境與無特權使用者 (Non-root User)。
# Why: 這是資安 (SecOps) 的最高指導原則。如果容器被駭客攻破，
#      他們也只會取得 nextjs 這個受限帳號的權限，無法破壞底層 Host 系統。
# =====================================================================
FROM node:20-alpine AS runner
WORKDIR /app

ENV NODE_ENV=production

# 建立專屬的無特權使用者與群組
RUN addgroup --system --gid 1001 nodejs
RUN adduser --system --uid 1001 nextjs

# 只複製必要的靜態資源
COPY --from=builder /app/public ./public

# =====================================================================
# 🚨 核心修正區：Standalone 模式的複製路徑
# Design Decision: 自動將擁有權 (chown) 移交給非 root 使用者。
# Why: standalone 資料夾包含了微型 Node.js 伺服器與精簡版的 node_modules。
#      .next/static 則是打包好的前端 JS/CSS 靜態資源。
# =====================================================================
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static

# 切換為非特權使用者
USER nextjs

EXPOSE 3000
ENV PORT=3000
ENV HOSTNAME="0.0.0.0"

# Standalone 模式的啟動入口不再是 'npm start'，而是直接用 node 執行 server.js
CMD ["node", "server.js"]