package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionMatchesReleaseMetadata(t *testing.T) {
	payload, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if metadataVersion := strings.TrimSpace(string(payload)); metadataVersion != version {
		t.Fatalf("VERSION 中的 %q 与运行时版本 %q 不一致", metadataVersion, version)
	}
}

func TestDecodeRequest(t *testing.T) {
	value, err := decodeRequest(strings.NewReader(`{"protocol":"v-local-cli-asr/1","action":"transcribe","audio_path":"sample.wav","source_audio_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sample_rate":16000,"channels":1,"language":"zh","model_path":"model"}`))
	if err != nil || value.Language != "zh" {
		t.Fatalf("协议请求解析失败：value=%+v err=%v", value, err)
	}
	if _, err := decodeRequest(strings.NewReader(`{"protocol":"v-local-cli-asr/1","action":"transcribe","audio_path":"sample.wav","sample_rate":24000,"channels":1,"model_path":"model"}`)); err == nil {
		t.Fatal("不应接受错误采样率")
	}
	if _, err := decodeRequest(strings.NewReader(`{"protocol":"v-local-cli-asr/1","action":"transcribe","audio_path":"sample.wav","source_audio_sha256":"digest","sample_rate":16000,"channels":1,"language":"zh","model_path":"model"}`)); err == nil {
		t.Fatal("不应接受无法作为来源证据的音频摘要")
	}
	if _, err := decodeRequest(strings.NewReader(`{"protocol":"v-local-cli-asr/1","action":"transcribe","audio_path":"sample.wav","source_audio_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sample_rate":16000,"channels":1,"language":"invalid","model_path":"model"}`)); err == nil {
		t.Fatal("不应把未知语言静默改成 auto")
	}
}

func TestNormalizeLanguage(t *testing.T) {
	if value, valid := normalizeRequestedLanguage("yue"); !valid || value != "yue" {
		t.Fatal("已知语言归一化错误")
	}
	if _, valid := normalizeRequestedLanguage("invalid"); valid {
		t.Fatal("语言归一化错误")
	}
}

func TestNormalizeObservedLanguagePrefersRecognizerEvidence(t *testing.T) {
	if got := normalizeObservedLanguage("<|yue|>", "zh"); got != "yue" {
		t.Fatalf("recognizer language was replaced by request language: %q", got)
	}
	if got := normalizeObservedLanguage("", "auto"); got != "unknown" {
		t.Fatalf("auto request was presented as detected language: %q", got)
	}
	if got := normalizeObservedLanguage("", "zh"); got != "zh" {
		t.Fatalf("explicit request fallback was lost: %q", got)
	}
}

func TestModelIdentityBindsActualModelAndTokens(t *testing.T) {
	root := t.TempDir()
	model := filepath.Join(root, "model.int8.onnx")
	tokens := filepath.Join(root, "tokens.txt")
	if err := os.WriteFile(model, []byte("model-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokens, []byte("tokens-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := modelIdentity(model, tokens)
	if err != nil || !strings.HasPrefix(first, "sensevoice-int8@sha256:") || len(strings.TrimPrefix(first, "sensevoice-int8@sha256:")) != 64 {
		t.Fatalf("model identity missing: value=%q err=%v", first, err)
	}
	if err := os.WriteFile(tokens, []byte("tokens-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := modelIdentity(model, tokens)
	if err != nil || second == first {
		t.Fatalf("model identity did not bind token content: first=%q second=%q err=%v", first, second, err)
	}
}

func useModelIdentityCache(t *testing.T, minBytes int64) string {
	t.Helper()
	cache := t.TempDir()
	previousDir, previousMin := modelIdentityCacheDir, modelIdentityCacheMinBytes
	t.Cleanup(func() { modelIdentityCacheDir, modelIdentityCacheMinBytes = previousDir, previousMin })
	modelIdentityCacheDir = func() (string, error) { return cache, nil }
	modelIdentityCacheMinBytes = minBytes
	return cache
}

// 适配器每条语音消息启动一次，模型有数百 MB，全量哈希必须能被签名缓存跳过。
func TestModelIdentityCachesLargeInputsBySignature(t *testing.T) {
	useModelIdentityCache(t, 16)
	root := t.TempDir()
	model := filepath.Join(root, "model.int8.onnx")
	tokens := filepath.Join(root, "tokens.txt")
	if err := os.WriteFile(model, []byte("model-content-aaaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokens, []byte("tokens-content-aa"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := modelIdentity(model, tokens)
	if err != nil {
		t.Fatal(err)
	}

	// 改写内容但保持大小与 mtime 不变：只有真的读了缓存才会返回同一个标识。
	info, err := os.Stat(model)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte("model-content-bbbb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(model, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	cached, err := modelIdentity(model, tokens)
	if err != nil {
		t.Fatal(err)
	}
	if cached != first {
		t.Fatalf("大输入没有命中签名缓存：%q != %q", cached, first)
	}

	// 签名变化（这里是大小）必须让缓存失效并重新计算。
	if err := os.WriteFile(model, []byte("model-content-bbbb-longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := modelIdentity(model, tokens)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("输入签名变化后仍返回了旧的模型身份")
	}
}

// 小输入必须绕过缓存：哈希本来就便宜，而 mtime 粒度不足以在连续写入之间区分内容。
func TestModelIdentitySkipsCacheForSmallInputs(t *testing.T) {
	cache := useModelIdentityCache(t, modelIdentityCacheMinBytes)
	root := t.TempDir()
	model := filepath.Join(root, "model.int8.onnx")
	tokens := filepath.Join(root, "tokens.txt")
	if err := os.WriteFile(model, []byte("model-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokens, []byte("tokens-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := modelIdentity(model, tokens); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("小输入不应写入模型身份缓存：%v", entries)
	}
}
