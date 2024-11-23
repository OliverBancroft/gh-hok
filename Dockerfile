FROM golang:1.23.3-alpine AS builder

WORKDIR /app

# 安装必要的工具
RUN apk add --no-cache git

# 复制 go.mod 和 go.sum
COPY go.mod go.sum ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY main.go .

# 构建
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o gh-hok

# 使用 scratch 作为最终镜像
FROM scratch

# 从 builder 复制 CA 证书
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# 复制二进制文件和配置
COPY --from=builder /app/gh-hok /


# 暴露端口
EXPOSE 8080

# 设置入口点
ENTRYPOINT ["/gh-hok"]