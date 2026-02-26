# ----------------------------------
# Stage 1: Builder (編譯階段)
# ----------------------------------
FROM golang:alpine AS builder

# 安裝必要的系統套件 (git 等，有些 Go 套件下載會用到)
RUN apk update && apk add --no-cache git

# 設定工作目錄
WORKDIR /app

# 先複製 go.mod 和 go.sum (如果有) 並下載依賴
# 使用星號確保即使本地還沒有 go.sum 也不會報錯
COPY go.mod go.sum* ./
RUN go mod download

# 複製所有原始碼
COPY . .

# 編譯 Go 程式碼
# CGO_ENABLED=0: 靜態編譯，確保執行檔可以在沒有 C standard library 的 Alpine 中運行
# GOOS=linux: 指定作業系統為 Linux
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

# ----------------------------------
# Stage 2: Runner (運行階段)
# ----------------------------------
FROM alpine:latest

# ⚠️ 關鍵：安裝根憑證 (ca-certificates)
# 因為我們的程式需要透過 HTTPS 呼叫 Etherscan API，沒有這個會報 x509 certificate 錯誤
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# 從 Builder 階段把編譯好的 main 執行檔複製過來
COPY --from=builder /app/main .

# 暴露 Port
EXPOSE 3000

# 啟動應用程式
CMD ["./main"]