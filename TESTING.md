# 首阶段验证记录

## 最新：Electron 桌面悬浮窗

已实现并运行 Electron 45.3.2 原生窗。下文“尚无桌面端”等描述是第一阶段历史记录，以此节为准。本轮未额外发起付费云请求；窗口已接收到用户在 Chrome 发起的真实会话日文和中文。

| 场景 / 验收点 | 数据及构造理由 | 如何执行 | 必要性 | 通过判断 | 实际结果 |
|---|---|---|---|---|---|
| 当前会话发现 | 真实 Go HTTP 创建并查询 current | 既有 HTTP 集成场景新增断言 | 桌面不能依赖手填会话 ID | 返回创建的会话标识 | 通过，Go 全量 race 通过 |
| 桌面适配器 | 本机 HTTP/WS 服务校验 cookie、Origin 和订阅路径 | desktop-backend.test.mjs | 验证主进程建会话与接收事件 | 收到预期中文事件 | 通过；仅适配器测试，Go 另有真实接口测试 |
| 字幕状态 | 快照、中文增量、旧序号和换会话 | desktop.test.mjs | 防止旧会话覆盖字幕 | 快照恢复、拒绝旧事件、换会话清空 | 通过 |
| 设置与后端地址 | 越界/非法值、已断开显示器的窗口位置、外网地址 | desktop.test.mjs | 防止窗口不可见、参数失控或外连 | 归一化设置、约束可见位置、拒绝非本机地址 | 通过 |
| 原生置顶与字幕 | 用户当前 Chrome 实际会话 | macOS 原生观察和 isAlwaysOnTop | 网页模拟不能证明原生行为 | 置顶 true，显示日文和中文 | 通过 |
| 控件与保存 | 双语/中文切换、字号32到33、背景95%到94%，随后恢复 | 原生控件操作并重启 Electron | 验证控件不受拖动区域拦截、设置持久化 | 日文隐藏/恢复、数值变化、重启恢复 | 通过 |
| 拖动与位置 | 标题和字幕使用原生 app-region，控件 no-drag | 界面观察、设置文件检查 | 验证移动和保存位置 | 位置改变并保存 | 已观察位置保存变化；自动化坐标拖动未能稳定复现，需手动再次验收 |

`npm test` 共 7 项通过，`go test -race ./...`、`go vet ./...` 和三个平台 Go 后端编译通过。macOS 窗口已运行，Windows 桌面尚未实机验收；独占全屏及多显示器切换仍需后续测试。

用户当前采集会话后来提示“音频发送积压超过一秒”，窗口保留末条字幕并显示后端错误。重新测试需在 Chrome 开始新会话。

## Chelsie 音色错误修复

初始化此前未显式指定音色。针对 `Voice Chelsie is not supported`，现设置 `session.voice=Tina`、`session.audio.output.voice=Tina`，关闭声音复刻，并保持纯文本输出。

| 场景 / 验收点 | 数据及构造理由 | 如何执行 | 必要性 | 通过判断 | 实际结果 |
|---|---|---|---|---|---|
| 覆盖不支持的默认音色 | 本机 TLS 服务返回 Chelsie 默认会话，检查初始化参数 | TestLiveTranslateOverridesUnsupportedDefaultVoice | 防止依赖服务端默认值再次触发错误 | 两个音色字段均为 Tina，声音复刻关闭，仅文字输出，握手成功 | 通过 |
| 真实云端修复验证 | 同一段 6.9 秒合成日语，连续两次调用 | TestRealLiveTranslatePipeline，-count=2 | 确认云端接受参数且能实际返回双语 | 两次均正常结束，日文与中文完整 | 通过，分别 9.33 秒、9.32 秒，含音频推送 |

修复后 Go 全量竞态回归通过，本地 17831 服务已使用新版本重启。

日期：2026-11-15。环境：macOS 26.5.1 / arm64、Go 1.27.3、Node.js 26.12.1、本机 Google Chrome。

## 验证边界

首轮使用下述本机云接口替身验收；随后经用户明确授权，已使用 qwen3.8-livetranslate-flash-realtime 做真实云端实测，见下方新增记录。默认自动测试仍不读取真实 Key。

自动化集成测试通过真实本地 HTTP/WebSocket 接口，经过实际 Go 会话、音频校验、ASR 协议客户端和翻译协议客户端；**只有外部云服务边界由本机 TLS 测试服务替代**。固定日文/中文输出不代表真实模型识别或翻译结果。

真实 Chrome 测试使用 48 kHz 合成测试音，通过真正的标签页共享和 AudioWorklet 采集，检查 Go 收到非零的 16 kHz PCM。测试页面明确标记了固定测试数据。

## 场景与结果

| 场景 / 验收点 | 数据及构造理由 | 如何调用或执行 | 必要性 | 预期与验证方式 | 实际结果 |
|---|---|---|---|---|---|
| 前端帧连续性 | 125 个 128 样本的工作线程块，共 1 秒恒定音频 | `node --test tests/*.test.mjs` | 防止分块时丢样本、重复样本或时间线漂移 | 恰好 10 个 100 ms 帧，每帧 3212 字节，序号及首样本位置连续，PCM 振幅正确 | 通过 |
| 混音、振幅和尾帧 | 双声道反相、超限振幅、NaN 和不足一帧的结尾 | 同上 | 发现声道处理错误、溢出和最后不足 100 ms 的音频丢失 | 反相归零、正负限幅正确、非法数值归零、尾帧只发送一次 | 通过 |
| 静音与长时间线 | 无输入声道；采样起点大于 32 位无符号范围 | 同上 | 静音不能停止音频时钟，长会话不能发生索引截断 | 输出静音样本，64 位索引不截断 | 通过 |
| Go 音频协议与有界缓冲 | 重复序号、错误采样位置、尾部短帧、超过 10 帧的未消费数据 | `TestAudioSequencePartialTailAndBoundedBuffer` | 防止两端格式不一致、重复发送和持续积压 | 拒绝非连续帧和尾帧后继续发送；积压超过 1 秒显式失败；失败帧不改变已接收时长 | 通过 |
| 地域与本地配置 | 北京业务空间地址、新加坡兼容地址、公共监听地址；带引号的字面 `.env` 值 | `TestConfigEndpointsAndLoopback`、`TestEnvFileIsLiteralAndExportedValuesWin` | 防止地域/域名错误、公网暴露和配置文件执行 shell 表达式 | 地址匹配地域，拒绝 `0.0.0.0`，已导出变量优先，配置字符串保持字面值 | 通过 |
| 音频 → ASR → 翻译 → 停止 | 通过 HTTP 创建会话，发送 2 个真实二进制 PCM 帧；云边界返回两次中间预览、两个最终句子及一次重复最终事件，尾句只在 `session.finish` 后返回 | `TestHTTPAudioToASRTranslationAndTailDrain/translation_status_0` | 同时验证真实接口流转、协议字段、去重和尾句，不仅检查 HTTP 状态 | 云端收到 6400 字节 PCM；中间结果未翻译；两句合并翻译一次；日文与中文正确关联；最终 stopped、200 ms、Token 统计正确；重复 stop 成功 | 通过 |
| 翻译限流 | 同上，翻译云边界返回 HTTP 429 | `TestHTTPAudioToASRTranslationAndTailDrain/translation_status_429` | 避免限流时日文消失、会话卡死或无限重试 | 保留日文，字幕状态为 translation_failed，显示 429，仍正常结束会话 | 通过 |
| 未配置 Key、跨站与 Host | 无 Key；不同 Origin；非回环 Host | `TestMissingKeyAndOriginIsolation` | 避免不明确的启动失败，以及外部网页跨站控制本机采集 | 创建会话返回 503 并说明配置项；不同来源和 Host 返回 403 | 通过 |
| 云端拒绝 Key | 本机 ASR 云边界握手返回 401 | `TestRejectedCloudKeyFailsSessionWithoutLeakingSecrets` | 防止无效 Key 后仍显示正在运行或返回敏感凭据 | 音频通道收到明确错误，最终会话 failed，本地响应不包含测试 Key | 通过 |
| 真实 Chrome 标签页采集 | 本机 Chrome 的 48 kHz 合成音源，共享到原型；日文/中文仍由本机云边界测试替身返回 | `CAPTION_BROWSER_TEST=1 go test -count=1 -run TestInteractiveChromeCapture -v ./internal/server`，通过 Chrome 原生共享选择器操作 | 单元测试无法证明浏览器真的授权、采样、重采样并发送音频 | Go 收到连续帧且含非零 PCM；界面显示输入音量、日文与中文；手动停止后释放共享并进入 stopped | 通过；收到 137,888 ms 音频，nonzero_pcm=true |

首次通过后台浏览器标签页触发共享时，Chrome 返回 InvalidStateError；切换到前台后，原生共享流程正常完成。界面已为该错误增加中文提示。该权限要求没有通过模拟 `getDisplayMedia` 绕过。

## 检查命令

```sh
go test -race -count=1 -v ./...
go vet ./...
node --test tests/*.test.mjs
node --check internal/web/static/app.js
```

- Go 功能/集成测试及竞态检查通过。
- Node 3 项 PCM 测试通过。
- Go vet、前端 JavaScript 语法检查通过。
- 人工 Chrome 采集测试单独运行并通过；常规 `go test` 默认跳过这项需人工共享操作的测试。
- Go 后端已成功构建 macOS arm64、macOS amd64 和 Windows amd64 可执行文件，位于忽略提交的 `bin/`。

编译命令：

```sh
go build -o bin/captiond ./cmd/captiond
GOOS=darwin GOARCH=amd64 go build -o bin/captiond-darwin-amd64 ./cmd/captiond
GOOS=windows GOARCH=amd64 go build -o bin/captiond-windows-amd64.exe ./cmd/captiond
```

Codex 环境对临时端口监听有沙箱限制，集成测试与本机服务在获得本机端口权限后执行；这与程序正常运行所需的网络访问相区别。

## 待真实 Key 验证

以下项目未执行，不能由固定字幕或协议测试的通过推断：

- 阿里云真实模型的账户权限、额度、地域连通性和实际返回兼容性。
- 真实日语识别准确率、简中翻译质量、专有名词表现。
- 从真实短句结束到完整中文显示的端到端延迟，含断句、网络、ASR、翻译和渲染。`translate_ms` 仅为翻译 HTTP 请求耗时。
- Windows 上的浏览器采集，以及两平台各 60 分钟的连续运行验收。Windows 本轮只验证了 Go 编译。

本地配置 Key 后建议先播放清晰日语口播 1–2 分钟，再检查连续字幕、背景音、长句及停止时的尾句。原型一次只翻译最终短句，连续无停顿长讲话的等待时间需单独记录。

Electron 原生桌面悬浮窗属于后续阶段，当前交付不包含其实现或验证。

## 浏览器验证截图

截图中顶部明确标记云接口已替换，文字为固定测试数据。图片用于证明真实页面、输入音量和字幕展示，不用于证明真实云模型识别效果。

![Chrome 真实采集验证，ASR 与翻译为固定测试数据](test-results/chrome-capture-full.jpg)

## LiveTranslate 真实云端实测（新增）

- 模型：`qwen3.8-livetranslate-flash-realtime`，北京地域，纯文本输出，目标语言 `zh`。
- 数据：本机 Kyoko 合成 6,891 ms 日语，16 kHz 单声道 PCM16，另追加 2 秒静音完成断句。
- 原文：こんにちは。今日はいい天気です。日本語の音声を中国語に翻訳するテストです。
- 真实识别：こんにちは 今日はいい天気です。 日本語の音声を中国語に翻訳するテストです。
- 真实译文：你好。今天天气很好。这是一个将日语音频翻译成中文的测试。
- 独立 Go 云协议探针：通过，收到原文/译文增量、完整文本、response.done(completed) 和 session.finished；耗时 9,065 ms（包括推送音频和额外静音，不能解释为翻译延迟）。
- 网页使用的真实 HTTP/WebSocket → Go 会话 → 阿里云 → 字幕快照：通过，TestRealLiveTranslatePipeline 9.33 秒，状态 stopped，日文和中文关联完整。首次该集成调用曾提前中断，原因未确定；重试通过，因此尚不能宣称稳定性已验收。
- 真实云端测试只使用上述预制测试音频；本轮未重新执行 Chrome 共享真实日语视频到云端的实机操作，Chrome 采集仍以上一轮验证为依据。
- 独立探针末次累计用量：input_tokens=96（其中 audio_tokens=49）、output_tokens=24。
- Key 仅从私有临时文件读取，测试报告和源代码不含密钥。
- 测试报告：`test-results/livetranslate-probe.json`、`test-results/livetranslate-app-real.json`。

新增验收用例：

| 场景 / 验收点 | 数据及构造理由 | 如何执行 | 必要性 | 通过判断 | 实际结果 |
|---|---|---|---|---|---|
| LiveTranslate 真实后端集成 | 已知原文的 6.9 秒合成日语，经实际 HTTP/WS 接口传入 | 显式设置 CAPTION_REAL_TEST=1、CAPTION_REAL_KEY_FILE、CAPTION_REAL_WAV 后运行 TestRealLiveTranslatePipeline | 确认新协议、模型权限、双语关联和结束流程，不能由替身测试推断 | 云端返回两种文字、字幕源文关联正确、会话 stopped | 重试通过；首次中断原因未定 |

后续仍需测试真实视频、人声/背景音乐、长句、中文繁简体一致性、真实句末到译文显示延迟，以及 Windows 实机和长时间稳定性。

LiveTranslate 接入后，Go 全量 `go test -race -count=1 ./...`、`go vet ./...` 和 3 项前端 PCM 测试通过。新增 `TestDirectTranslationDeltasAndFinalAssociation` 验证译文增量累加、源文关联以及重复完成事件去重（随后单独执行竞态测试通过）。
