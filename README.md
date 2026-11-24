# AegisGate

使用 Go 标准库构建的轻量 API 网关，提供可复现的功能与故障验证。

首期实现：YAML 配置、按路径段匹配的最长前缀路由、双节点轮询、HTTP 反向代理、连接池、超时、请求体上限、请求 ID、结构化访问日志和优雅停机。**尚未实现限流、WAF、健康摘除、熔断、配置热更新和监控看板。** 未进行吞吐量压测，不宣称生产就绪或攻击防护效果。

## 环境要求

- Go 1.24+。
- Linux 或 WSL 的 Bash、Python 3；真实进程停机实验使用 POSIX 信号。
- 竞态检测需 C 编译器和 CGO；make 可选。

## 构建和验证

进入 clone 后的仓库目录运行：

```bash
cd aegisgate
bash scripts/dev.sh build
bash scripts/dev.sh check
bash scripts/dev.sh smoke
```

不需要安装 make。Makefile 只是这些命令的可选别名。首次构建需下载 YAML 依赖，版本及校验和固定在 go.mod / go.sum。

`check` 检查格式、执行 go vet 和全部单元/HTTP 集成测试。
`smoke` 启动真实二进制，使用临时回环端口验证双上游转发、请求头处理、Body 上限及 SIGINT 排空慢请求；退出时清理自己的进程和临时文件。

```bash
bash scripts/dev.sh race
```

竞态检测需要 C 编译器及启用 CGO。安装编译器后可使用 `CGO_ENABLED=1` 执行上述命令；脚本不会自动修改系统工具链。

## 手动体验

构建后，打开三个 WSL 终端，都先进入源码目录，再分别运行：

```bash
# 终端一
./bin/mock-upstream -listen 127.0.0.1:18081 -name node-a

# 终端二
./bin/mock-upstream -listen 127.0.0.1:18082 -name node-b

# 终端三
./bin/gateway -config configs/gateway.yaml
```

在第四个终端观察两个节点交替响应：

```bash
curl -i http://127.0.0.1:8080/hello
curl -i http://127.0.0.1:8080/hello
curl -i 'http://127.0.0.1:8080/echo?q=hello' -d 'hello'
curl -i http://127.0.0.1:8080/healthz
```

结束时对三个进程分别按 Ctrl+C。演示默认仅监听回环地址，不创建常驻服务。
端口占用时换端口并修改上游配置，勿停止其他项目进程。

Mock 支持 `-delay 500ms`、`-error-rate 0.2`，以及不受模拟延迟和故障率影响的 `/healthz`。
网关配置可先单独验证：

```bash
./bin/gateway -check -config configs/gateway.yaml
```

## 请求如何经过网关

```text
http.Server -> request ID / access log
            -> longest segment-prefix route
            -> body limit + request deadline
            -> atomic round-robin
            -> httputil.ReverseProxy + shared http.Transport
            -> upstream
```

- 路由配置 `/api` 匹配 `/api` 和 `/api/users`，不匹配 `/apix`。更长的前缀优先；当前所有 HTTP 方法使用相同路由。
- 转发保留路径和查询参数，不剥离前缀。上游地址当前只支持 HTTP(S) origin，不接受凭据、子路径、查询或片段。
- `/healthz` 是网关保留的本地健康端点，仅表示进程可响应，不代表上游健康。
- 网关默认直接面对客户端：丢弃传入 Forwarded / X-Forwarded-*，从实际连接重建转发头，并重新生成 X-Request-ID。尚无可信前置代理配置。
- 已知 Content-Length 超限时提前返回 413；未知长度请求通过流式读取限制，超限时可能已有一部分数据抵达上游。这不是“完整安全检查后再放行”。
- 连接或转发失败返回 502；响应头/请求期限超时在尚未发送响应时返回 504；上游 HTTP 状态码原样返回。响应已开始发送后的流错误无法改写状态码，客户端可能收到截断响应。
- 没有应用层重试，故障节点仍会轮询到；健康检查、熔断与恢复留到后续阶段。
- 默认访问日志记录请求 ID、路由、方法、状态、字节数和耗时，不记录请求体、查询参数和认证头。

## 为什么这样实现

使用标准库 ReverseProxy 处理 HTTP 转发细节，把学习重点放在连接池、资源边界、路由策略与生命周期。首期路由用排序后的切片，便于验证语义；之后若实现 Radix Tree，可在一致语义下比较基准结果。

每个上游池只有一个原子轮询计数器，路由表启动后不变，共享 Transport 负责连接复用。先不引入配置快照、插件框架和对象池，等对应需求出现再扩展。

收到停机信号后停止接入新请求，再用独立的有期限上下文调用 Shutdown；不把进程信号上下文设为请求 BaseContext，避免提前取消在途请求。超过宽限期才强制关闭连接。默认请求期限 20 秒、停机宽限 25 秒；这些值可配置，长流和 WebSocket 不属于首期验收范围。

接口说明：[ReverseProxy](https://pkg.go.dev/net/http/httputil#ReverseProxy)、[Server.Shutdown](https://pkg.go.dev/net/http#Server.Shutdown)。

## 目录

```text
cmd/gateway/          网关命令、配置及信号入口
cmd/mock-upstream/    本地实验上游
internal/config/     YAML 默认值、严格解析与校验
internal/gateway/    路由、轮询、代理和请求边界
internal/server/     监听及有期限的优雅停机
internal/mockupstream/ Mock HTTP Handler
configs/             可运行配置
scripts/             开发命令和真实进程冒烟测试
```
