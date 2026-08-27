# 发布流程

正式发布只从版本标签触发 `.github/workflows/release.yml`。标签必须与 `VERSION`
和二进制 `--version` 完全一致；当前预发布标签为 `v0.1.0-dev.0`。

## 一次性配置

在 GitHub `release` environment 中配置并保护：

- `WINDOWS_SIGNING_CERTIFICATE_BASE64`
- `WINDOWS_SIGNING_CERTIFICATE_PASSWORD`

证书必须可用于 Authenticode 代码签名。正式工作流会签名并验证 EXE 和三个 DLL，
且要求可信时间戳；缺少任一条件时发布失败。

## 发布步骤

1. 在 `main` 上确认 `Audit gates` 全绿，并运行 `Release candidate`。
2. 下载候选 ZIP，在干净 Windows amd64 机器上验证 `--version`、协议请求、DLL 加载和真实离线转写，并记录候选 ZIP、EXE、DLL、模型与 tokens 的 SHA-256；该证据只绑定这组摘要。
3. 核对 ZIP 内项目许可证、Apache-2.0 副本、第三方声明、模型来源说明、组件摘要和 manifest。
4. 从已验收提交推送与 `VERSION` 一致的标签；`Signed release` 会重新构建和签名、生成来源证明并创建 GitHub prerelease。由于签名产物摘要与候选件不同，必须下载这个确切 prerelease，复验签名、manifest、DLL 加载、协议和一条脱敏真实离线转写，再把该证据绑定最终摘要后才能提升为正式 release。源码提交相同不能替代二进制级复验。

SenseVoice 模型始终独立获取，不进入本仓库 Release。
