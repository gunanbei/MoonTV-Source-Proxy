# Video Source Proxy

独立部署的轻量 Go 通用反向代理。每个请求通过 `proxy` 参数指定上游，代理会透明转发请求方法、请求体、请求头、Cookie、授权信息、路径和查询参数；响应状态码、响应头、Cookie 和非 JSON 内容也会原样返回。仅对合法、未压缩的 JSON 尝试将超长整数转成字符串。HTTP 标准规定的 hop-by-hop 头由 Go 的反向代理自动处理。

无 MoonTVPlus 依赖，无第三方 Go 依赖。只处理合法、未压缩且不超过 32 MiB 的 JSON；非 JSON、压缩 JSON、无效 JSON、超大 JSON 或处理失败时均原样透传，不会因为转换失败阻断上游响应。

## 编译运行

```bash
go build -trimpath -ldflags='-s -w' -o video-source-proxy .
./video-source-proxy
```

默认监听 `:8080`，可通过 `LISTEN_ADDR` 修改。健康检查：`/healthz`。

## 使用方式

代理入口始终是根地址。将完整上游 API 地址放入 `proxy` 参数：

```text
http://代理地址:8080/?proxy=https%3A%2F%2Fapi.example.com%2Fapi%2Fprovide%2Fvod%2F
```

请求路径和查询参数会接到上游地址后。例如：

```text
http://代理地址:8080/?proxy=https%3A%2F%2Fapi.example.com%2Fapi%2Fprovide%2Fvod%2F&ac=videolist&ids=123
```

代理请求上游：

```text
https://api.example.com/api/provide/vod/?ac=videolist&ids=123
```

## MoonTVPlus 配置

视频源 API 地址填写根入口格式：

```text
http://代理地址:8080/?proxy=https%3A%2F%2Fapi.juliang.live%2Fapi%2Fprovide%2Fvod%2F
```

应用后续拼接的 `?ac=...`、`?wd=...`、`?ids=...` 等参数会转发给上游。Detail 地址留空。播放返回的 `.m3u8` 地址仍由上游提供。

## Docker

```bash
docker build -t video-source-proxy .
docker run --rm -p 8080:8080 video-source-proxy
```

`proxy` 参数允许调用方选择目标主机，因此该服务是动态代理。请只在可信网络中使用，或在服务前配置 HTTPS、访问认证和网络访问控制，避免公开暴露。
