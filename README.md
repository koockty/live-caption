# Live Captions

个人使用的日语实时字幕原型。当前默认：**Chrome 标签页音频 → 本地 Go 后端 → qwen3.8-livetranslate-flash-realtime → 日文原文与简中译文 → 网页双语字幕**。

`CAPTION_PIPELINE=livetranslate` 使用同一个实时模型完成转写和翻译，只输出文字，不生成译音、不调用额外的翻译 API。源语言由模型自动检测，目标语言为 `zh`。已用约 6.9 秒本地合成日语音频通过真实阿里云调用，以及实际本地 HTTP/WebSocket 后端集成测试。结果见 `test-results/livetranslate-probe.json` 和 `test-results/livetranslate-app-real.json`。

原先的两阶段方案仍可通过 `CAPTION_PIPELINE=asr_mt` 使用。下面涉及独立 ASR、Qwen-MT、短句合并及翻译请求间隔的说明适用于该模式。

已实现 Chrome 采集链路和 Electron 原生置顶悬浮字幕窗。桌面端自动发现 Go 后端的当前会话并订阅字幕，不读取云端 Key。

## 桌面悬浮窗

保持 Go 后端运行，在另一个终端启动：

```sh
npm ci
npm run desktop
```

桌面开发运行需要 Node.js 22.12+。首次启动可能下载官方 Electron 运行时，当前电脑已安装。Chrome 仍负责选择和采集标签页，桌面端自动接收同一会话的字幕。

- 窗口置顶、无边框；macOS 配置为在所有工作区及全屏工作区可见。
- 点击“仅中文 / 双语显示”切换模式。
- 拖动左上角标识或字幕区域移动窗口，控件区域不拖动；也可从边缘调整大小。
- 字号范围 18–60 px，背景不透明度范围 15%–95%。透明度只改变背景，文字保持清晰。
- 模式、字号、背景和位置自动保存，重启恢复。设置位于 Electron userData 目录：macOS 通常是 `~/Library/Application Support/Live Captions/captions-settings.json`，Windows 通常是 `%APPDATA%/Live Captions/captions-settings.json`。
- 关闭字幕窗只退出桌面显示，不会停止 Chrome 采集或 Go 后端。
- “采集页”按钮使用系统默认浏览器打开页面；若不是 Chrome，请手动在 Chrome 打开。
- 默认连接 `http://127.0.0.1:17831`，其他本机端口通过 `CAPTION_BACKEND_URL` 设置，仅接受回环 HTTP 地址。

新增鉴权接口 `GET /v1/sessions/current`，返回当前 `snapshot`（无会话为 null）。桌面主进程负责本地 cookie 与 WebSocket，沙箱渲染进程通过有限 preload IPC 接收字幕和调整显示。

当前为可运行开发版，尚未制作安装包；Windows 桌面和不同全屏应用的行为仍需实机验证。

## 本地启动

需要 Go 1.24+、新版桌面 Chrome，以及阿里云百炼北京或新加坡地域的 API Key。网页资源随 Go 服务内嵌，无须安装 Node.js 或启动前端构建工具。

1. 将 `.env.example` 复制为 `.env`，填写 API Key：

   ```dotenv
   DASHSCOPE_API_KEY=在本地填写真实的百炼APIKey
   DASHSCOPE_REGION=cn-beijing
   CAPTION_PIPELINE=livetranslate
   DASHSCOPE_WORKSPACE_ID=
   CAPTION_LISTEN=127.0.0.1:17831
   ```

   Workspace ID 可选；填写后使用业务空间专属域名。不填写时使用该地域仍受支持的 DashScope 域名。Key、Workspace 和地域必须匹配。

   [获取百炼 API Key](https://help.aliyun.com/zh/model-studio/get-api-key)。默认模式只需要该 Key 有 qwen3.8-livetranslate-flash-realtime 的访问权限；asr_mt 模式才需要独立 ASR 和 Qwen-MT 的权限。

2. 在项目目录运行：

   ```sh
   go run ./cmd/captiond
   ```

   也可以直接设置 `DASHSCOPE_API_KEY` 环境变量。已导出的环境变量优先于 `.env`。`.env` 只按字面值读取，不执行 shell 命令。

3. 在 **Chrome** 打开 [http://127.0.0.1:17831](http://127.0.0.1:17831)。音源网页也需要在同一个 Chrome 中打开。
4. 点击“选择标签页并开始”，选择音源标签页，并勾选“共享标签页音频”。等待状态变为“正在识别”后播放日语内容。
5. 保持采集页打开。日文先显示实时预览；确认短句后显示中文。点击“停止并完成尾句”结束。

修改 Key 或地域后需要重新启动 Go 服务。没有 Key 时仍能查看页面，但“开始”按钮不可用；无效 Key 会在连接云服务时显示明确错误。

编译本机可执行文件：

```sh
go build -o bin/captiond ./cmd/captiond
```

Windows 可以使用 `go build -o bin/captiond.exe ./cmd/captiond`。程序在启动目录查找 `.env`；也可用 `-env /path/to/.env` 指定配置文件。

## 已实现

- 浏览器原生标签页音频共享；拒绝无音频或非标签页来源。
- 由 Chrome 的 16 kHz AudioContext 完成源音频重采样；AudioWorklet 混为单声道并编码 PCM16LE。
- 每 100 ms 一帧，带序号和 64 位采样位置；最后不足 100 ms 的尾帧也会发送。
- Go 使用 WebSocket 调用 `qwen3-asr-flash-realtime`，固定日语、16 kHz PCM、云端 VAD（600 ms）。
- 中间结果按 `text + stash` 替换显示；仅将最终短句交给 `qwen-mt-flash` 日译中。
- 相邻短句合并、翻译请求起始间隔至少 1.25 秒、最近三组翻译记忆；重复最终事件不重复翻译。
- 停止时排空已接收音频，发送 `session.finish`，等待尾句和翻译完成。
- 双语/仅中文、字号调整、输入音量、已发送时长、最近字幕；显示设置保存在浏览器，字幕仅在内存。
- 翻译失败保留日文；音频和任务队列均有上限，失败明确提示；字幕事件通道重连后恢复快照。
- 服务只绑定回环地址；同源验证、Host 验证、HttpOnly/SameSite cookie；云 Key 不进入网页、URL 或日志。

为保留音频轨道，`getDisplayMedia` 同时取得的视频轨道在会话结束前保持开启，但画面不渲染、不编码、不上传。音源原本的声音保持正常播放，采集图不会重复播放音频。

## 本轮范围与限制

- 已有 Electron 原生悬浮窗，但没有自动重新连接云端 ASR。音频或云端 ASR 连接中断后，会结束本次会话，由用户重新开始；事件显示通道可自动重连。
- asr_mt 模式仅翻译最终短句；livetranslate 模式接收模型的原文与译文增量。没有自然停顿的长讲话可能等待较久；从稳定前缀切分长句的优化留到取得真实日语基线后进行。
- 浏览器刷新或关闭采集页会结束采集；一个后端实例同时只允许一个活跃会话。
- 当前网页只检查 Key 是否已配置，真正的权限、网络和额度在开始连接时验证。
- `translate_ms` 只表示翻译 HTTP 请求耗时，**不是完整链路延迟**。目标“短句结束后 2–3 秒”需要在真实 Key、真实日语音频下另外测量。
- ASR 模型没有字词级转写时间戳。此原型不提供精确 SRT 字幕文件或网页视频播放进度同步。
- 我们的程序不将音频或字幕写入文件。云服务的数据处理规则以厂商服务条款为准。

## 本地 API

先访问 `/` 建立本机浏览器会话。API 使用 `lc_local` HttpOnly cookie；POST 与 WebSocket 要求 `Origin` 与本机地址完全一致。没有跨域访问接口。

| 方法 | 路径 | 行为 |
|---|---|---|
| GET | `/healthz` | 服务状态与是否已配置 Key，不返回凭据 |
| GET | `/v1/config` | 固定语言、模型和采样率 |
| POST | `/v1/sessions` | 正文 `{}`，创建会话；已有活跃会话返回 409 |
| GET | `/v1/sessions/{id}` | 当前字幕、状态、音频毫秒数和 Token 统计 |
| WS | `/v1/sessions/{id}/audio` | 等待 `{"type":"ready"}` 后发送二进制音频帧 |
| WS | `/v1/sessions/{id}/events` | 首次/重连先返回原子快照，其后推送带序号的事件 |
| POST | `/v1/sessions/{id}/stop` | 幂等停止，等待尾句与已排队翻译 |

音频帧（小端）：

```text
0..3    uint32 递增帧号，从 0 开始
4..11   uint64 首个样本位置，从 0 开始
12..    PCM16LE，通常 1600 个样本 / 3200 字节
```

允许最后一帧少于 1600 个样本。发送完所有音频后，发送文本事件 `{"type":"audio.end"}`，**不要先调用停止接口再发送尾帧**。服务端会完成最后识别并推送 `stopped`。事件中用 `session_id + caption.id + revision` 更新字幕，不要逐条追加 ASR 完整中间文本。

会话状态为 `created → connecting → running → finishing → stopped`，错误时为 `failed`。新会话使用不同 ID，上一会话不会污染新会话。

## 验证

```sh
go test -race ./...
go vet ./...
node --test tests/*.test.mjs
```

Go 集成测试经过真实本地 HTTP/WebSocket、Go 会话、云协议客户端，再到本机 TLS 云接口测试替身。测试替身是**唯一被替代的外部边界**，返回固定日文/中文，用于协议和状态验收；它不验证阿里云真实模型的识别准确率、翻译质量或线上延迟，也不调用付费服务。

另有人工 Chrome 采集测试入口，只存在于测试代码中：

```sh
CAPTION_BROWSER_TEST=1 go test -count=1 -run TestInteractiveChromeCapture -v ./internal/server
```

启动后，打开 `http://127.0.0.1:17832/test-tone` 播放测试音，再打开 `http://127.0.0.1:17832/`，共享该测试音标签页，看到固定字幕后点击停止。此测试检查 **真实 Chrome/AudioWorklet 采集是否向 Go 传入非零 PCM**。页面会明确标记“固定测试数据”，没有生产 mock 开关。

测试场景与实际结果见 [TESTING.md](TESTING.md)。

## 实现依据

- [Chrome 标签页共享与音频](https://developer.chrome.com/blog/avoiding-oversharing-when-screen-sharing)
- [Qwen ASR WebSocket 接入](https://help.aliyun.com/zh/model-studio/qwen-asr-realtime-interaction-process)
- [Qwen ASR 客户端事件](https://help.aliyun.com/zh/model-studio/qwen-asr-realtime-client-events)
- [Qwen ASR 服务端事件](https://help.aliyun.com/zh/model-studio/qwen-asr-realtime-server-events)
- [Qwen MT API](https://help.aliyun.com/zh/model-studio/qwen-mt-api)
