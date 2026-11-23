# quick2api

把 **Amazon Quick** 办公 AI 背后的模型接入 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 的原生动态库插件。

通过浏览器 OAuth（OIDC + PKCE）直连 Quick 云端，**不需要安装 Amazon Quick 桌面端**，也不需要绑信用卡。接入后任意 OpenAI / Anthropic 兼容客户端都能直接调用。

## 支持的模型

| 模型 ID | 档位 | 上游 |
|---|---|---|
| `quick2api-quick-smart` | 智能 | Claude Opus 5.5（约 1M 上下文） |
| `quick2api-quick-balanced` | 均衡 | Sonnet 级 |
| `quick2api-quick-fast` | 快速 | Haiku 级 |
| `quick2api-quick-advanced` | 进阶 | 服务端路由 |

调用时 `usage.total_tokens` 约 69k 是 Quick 内置系统提示的特征值，可据此确认请求打到了真实上游。

## 功能

- 浏览器 OAuth 登录（免桌面端），token 过期自动用 refresh_token 续期
- 完整支持流式与非流式（OpenAI `chat/completions` 接口面，Claude/Gemini 客户端由宿主转换）
- 内置 Web 控制台：账号卡片、模型列表、别名编辑、启停、删除
- 管理密钥自动复用 CPA 管理面板已保存的密钥
- 远程/手机场景支持「粘贴回调链接」兜底
- 与同宿主下的其它插件（如 freetier）provider 完全隔离

## 安装

1. 下载 Release 中的 `quick2api-local-install.zip`（zip 根目录仅含 `quick2api.dll`）
2. 在 EasyCLIProxyAPI 的插件页用「本地上传安装」选择该 zip
3. 或手动把 `quick2api.dll` 放到：

```
<cpa-core>/plugins/windows/amd64/quick2api.dll
```

4. 在 `config.yaml` 中启用：

```yaml
plugins:
  enabled: true
  configs:
    quick2api:
      enabled: true
```

5. 重启核心

## 添加账号

在控制台点「添加账号」，浏览器会打开 Amazon 授权页，完成登录后凭证自动落盘到 `oauth` 目录。

> **登录必须在跑插件的这台机器的浏览器里完成。** AWS OIDC 只接受 loopback（`127.0.0.1`）回调，
> 实测传局域网 IP 会被拒：`invalid_redirect_uri: Requested client type must use loopback interface for redirect`。
> 手机或远程部署请用登录框里的「粘贴回调链接」。

## 调用

```bash
curl http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer <CPA_API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"model":"quick2api-quick-smart","messages":[{"role":"user","content":"hi"}],"stream":true}'
```

## 构建

需要 Go 1.22+ 与 C 编译器（`CGO_ENABLED=1`），Windows 下用 mingw-w64：

```bash
CGO_ENABLED=1 go build -buildmode=c-shared -ldflags="-s -w" -o dist/quick2api.dll .
```

`build.ps1` 是 Windows 下的封装脚本。注意：mingw 的 `ld.exe` 处理不了含非 ASCII 字符的路径，
构建目录建议放在纯 ASCII 路径下（例如 `C:\q2abuild`），否则链接阶段会报找不到 `dllcrt2.o`。

## 实现要点

这个插件的 C ABI 骨架派生自 CLIProxyAPI 插件生态，协议层是独立逆向实现的：

1. **会话获取**：OIDC `client/register`（必须带 `issuerUrl`，否则 400 `Start URL is required`）→ `authorize`（PKCE S256）→ `token`
2. **聊天通道**：`wss://<ws_host>/streaming/quickwork-chat-bidirectional-stream`，上行是二进制帧承载 base64 包裹的 JSON，下行是 AWS event-stream
3. **握手**：`session_connect` 必须带一整套 capability flags，`outpostId` 需稳定 UUID，WS 头需 `Origin: https://quick.aws.com`
4. **档位选择**：通过 `user_message.agent_mode` 指定，`turn_usage.resolved_model_id` 回传真实模型
5. **帧上限**：单条 `user_message` 约 64KB，超长需走附件通道或分片

几个踩过的坑写在源码注释里，主要是字段大小写（AWS 返回 `clientId` 而非 `client_id`）、
失败时先写响应再关服务（否则用户只看到连接被拒）、以及 provider key 必须独立（否则会抢走同宿主其它插件的凭证认领权）。

## 已知限制

- 工具调用是文本约定模拟（`<tool_call>{...}</tool_call>`），Quick 协议没有原生客户端工具通道
- 单条 `user_message` 约 64KB 上限，超长 prompt 走分片，会放大成多轮上游调用
- Quick 客户端协议更新后可能失配，需要跟进适配
- 仅支持 Windows（动态库 + 本地回调服务的组合限制）；协议层本身跨平台

## 致谢

本项目站在这些工作的肩膀上：

- **[jiandanc/CLIProxyAPI-freetier2api-plugin](https://github.com/jiandanc/CLIProxyAPI-freetier2api-plugin)**（MIT）
  与 **[cxy2730 的维护分支](https://github.com/cxy2730/CLIProxyAPI-freetier2api-plugin)**（MIT）
  —— 插件 C ABI 骨架、宿主回调封装、管理接口框架、控制台页面结构均派生于此。
  本项目的 `cpasdk/`、`internal/hostbridge/`、`internal/app/`、`internal/core/` 大量沿用其设计。
- **[httpdone/magpie-aws-quick](https://github.com/httpdone/magpie-aws-quick)**（MIT）
  —— Quick 的 OIDC 流程、AIDP WebSocket 帧格式、`agent_mode` 档位映射等协议事实，
  来自该项目公开的 `PROTOCOL.md` 与 `lib/oauth.mjs` 实现。
- **[router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)**（MIT）—— 插件宿主与 ABI 契约。

## 许可证

MIT，见 [LICENSE](LICENSE)。派生部分的原始版权归上述作者所有。
