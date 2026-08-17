# 真实语音验收记录

- 日期：2026-08-13
- 平台：Windows amd64
- 主 CLI 协议：`v-local-cli-asr/1`
- 引擎：sherpa-onnx SenseVoice CPU 离线识别
- 模型：SenseVoiceSmall int8
- 模型 SHA-256：`C71F0CE00BEC95B07744E116345E33D8CBBE08CEF896382CF907BF4B51A2CD51`
- tokens SHA-256：`F449EB28DC567533D7FA59BE34E2ABCA8784F771850C78A47FB731A31429A1DC`
- 结果：从当前只读微信快照中选择一条真实语音，经主 CLI 精确关联、SILK 解码和适配器转写后返回非空文字，并写入账号私有转写暂存。
- 耗时：约 7.2 秒。
- 隐私：记录不保存语音、转写正文、会话标识或联系人信息；临时 WAV 在调用结束后删除。
- 网络：适配器代码没有下载或网络请求路径，协议响应为 `network_used=false`。

该记录证明上述平台、运行时与模型组合完成过一次端到端验收，不代表所有微信版本、音质、语言或模型版本均具有相同准确率。
