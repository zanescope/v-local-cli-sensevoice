# v-local-cli-sensevoice

`v-local-cli-sensevoice` 是独立、可选的本地 SenseVoice 适配器，实现 `v-local-cli-asr/1` stdin/stdout JSON 协议。主程序 `v-local-cli` 不依赖本仓库，也不下载模型或原生运行时。

本适配器只在本机离线处理 `v-local-cli` 解码后交来的语音，**仅供处理本人拥有或已获明确授权访问**的数据；它由用户自行选择安装，不随主程序内置或分发。

当前实现使用 sherpa-onnx 官方 Go 绑定和 CPU 离线识别。构建时会引入 sherpa-onnx 的平台原生运行时；模型目录由用户显式提供，必须包含：

- `model.int8.onnx`
- `tokens.txt`

## 构建

```powershell
go test ./...
go build -trimpath -o build/v-local-cli-sensevoice.exe .
```

官方 Go 绑定需要 CGO。Windows 构建机需要可用的 MinGW-w64 C 编译器；推荐运行 `scripts/build.ps1`，它会测试、构建，并从锁定的 `v1.13.5` Go 模块复制三个必需的 DLL，最后输出 SHA-256。项目不分发模型，已验收模型的来源和摘要见 [MODEL_SOURCES.md](MODEL_SOURCES.md)。候选件与正式 Authenticode 发布步骤见 [RELEASING.md](RELEASING.md)。

## 接入

```powershell
v-local-cli voice-status --asr-provider <v-local-cli-sensevoice.exe> --model <SenseVoice模型目录>
v-local-cli voice-transcribe --asr-provider <v-local-cli-sensevoice.exe> --model <SenseVoice模型目录> <voice_evidence_id>
```

音频由 `v-local-cli` 解码成权限受限的 16 kHz 单声道临时 WAV，适配器读取后直接离线识别；两端都不持久保存该 WAV，适配器响应固定声明 `network_used=false`。

## 上游边界

- sherpa-onnx Go 绑定：<https://github.com/k2-fsa/sherpa-onnx-go>
- SenseVoice 官方说明：<https://k2-fsa.github.io/sherpa/onnx/sense-voice/index.html>
- 推荐模型：`sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17-int8`

本仓库不再分发这些上游文件；使用者需要遵守各自的许可证，并自行验证来源与摘要。

已完成的脱敏端到端验收见 [REAL_DEVICE_TEST.md](REAL_DEVICE_TEST.md)。它只覆盖记录在案的平台、运行时与模型组合，不把单次识别结果泛化为准确率保证。

## 许可

采用个人非商业许可（`v-local-cli-sensevoice Personal Non-Commercial License 1.0`），不是 OSI 批准的开源许可，且仅限用户对本人拥有或已获明确授权访问的数据使用。商业使用、再分发或作为服务提供需要另行取得书面许可，详见 [LICENSE](LICENSE)。上游 sherpa-onnx 运行时与 SenseVoice 模型另受各自许可约束，需要分别遵守。
