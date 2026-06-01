# 多阶段构建：用 golang 编译静态二进制，再放进极小的 alpine 运行。
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
# CGO_ENABLED=0 配合 modernc.org/sqlite（纯 Go）产出静态二进制。
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /ifgone ./cmd/ifgone

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 ifgone
COPY --from=build /ifgone /usr/local/bin/ifgone
USER ifgone
ENTRYPOINT ["ifgone", "--config", "/app/config.yaml"]
