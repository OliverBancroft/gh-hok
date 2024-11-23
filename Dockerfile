FROM golang:1.21-alpine AS builder

WORKDIR /app

# 复制源代码
COPY . .

# 下载依赖并构建
RUN go mod download && \
  CGO_ENABLED=0 GOOS=linux go build -o github-proxy

# 使用轻量级基础镜像
FROM alpine:latest

# 添加 CA 证书
RUN apk --no-cache add ca-certificates

WORKDIR /app

# 从 builder 阶段复制二进制文件
COPY --from=builder /app/github-proxy .
COPY --from=builder /app/blacklist.txt .

# 暴露端口
EXPOSE 8080

# 运行应用
ENTRYPOINT ["./github-proxy"]