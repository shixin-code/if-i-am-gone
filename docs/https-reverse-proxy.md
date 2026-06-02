# HTTPS 反代部署说明

更新时间：2026-06-02

self_hosted 下载链接必须通过公网 HTTPS 暴露给受益人。应用本身监听 `download.self_hosted.listen_port`，建议由 Nginx 或 Caddy 负责 TLS 证书和公网入口。

## 配置要求

- `download.mode: self_hosted`
- `download.self_hosted.public_base_url: https://your-domain.example.com`
- `download.self_hosted.listen_port: 8080`
- 防火墙开放 80/443，应用端口 8080 可仅允许本机访问。

## Caddy 示例

```caddyfile
your-domain.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy 会自动申请和续期 HTTPS 证书。

## Nginx 示例

```nginx
server {
    listen 80;
    server_name your-domain.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name your-domain.example.com;

    ssl_certificate /etc/letsencrypt/live/your-domain.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/your-domain.example.com/privkey.pem;

    location /download/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }
}
```

## 验证

- `curl -I https://your-domain.example.com/download/not-exist` 应返回 `404`，且响应包含 `Cache-Control: no-store`。
- 真实下载链接应返回 `200` 并下载 ZIP 文件。
- 下载次数达到 `download.max_downloads` 后应返回 `410 Gone`。
- 链接过期后应返回 `410 Gone`。

## 安全建议

- 不要把应用直接暴露为 HTTP 公网入口。
- 下载链接邮件中只放 HTTPS 地址。
- 不要在 Nginx/Caddy 日志中长期保留完整下载 token；如需更高安全性，应对 access log 做脱敏或缩短保存周期。
- 定期检查证书续期是否正常。
