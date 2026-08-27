package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const protocolName = "v-local-cli-asr/1"
const maxRequestBytes = 64 * 1024

var version = "0.1.0-dev.0"

type request struct {
	Protocol  string `json:"protocol"`
	Action    string `json:"action"`
	AudioPath string `json:"audio_path"`
	// SourceAudioSHA256 is a caller-supplied provenance marker for the original
	// SILK payload. The adapter only receives decoded WAV and therefore cannot
	// independently verify this digest.
	SourceAudioSHA256 string `json:"source_audio_sha256"`
	SampleRate        int    `json:"sample_rate"`
	Channels          int    `json:"channels"`
	Language          string `json:"language"`
	ModelPath         string `json:"model_path"`
}

type response struct {
	Protocol    string `json:"protocol"`
	Transcript  string `json:"transcript"`
	Engine      string `json:"engine"`
	Model       string `json:"model"`
	Language    string `json:"language"`
	NetworkUsed bool   `json:"network_used"`
}

func decodeRequest(reader io.Reader) (request, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, maxRequestBytes+1))
	decoder.DisallowUnknownFields()
	var value request
	if err := decoder.Decode(&value); err != nil {
		return request{}, errors.New("请求不是有效的受限 JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return request{}, errors.New("请求包含多余内容")
	}
	if value.Protocol != protocolName || value.Action != "transcribe" {
		return request{}, errors.New("协议或动作不匹配")
	}
	if value.SampleRate != 16000 || value.Channels != 1 {
		return request{}, errors.New("只接受 16 kHz 单声道 WAV")
	}
	value.AudioPath = strings.TrimSpace(value.AudioPath)
	value.ModelPath = strings.TrimSpace(value.ModelPath)
	normalizedLanguage, languageValid := normalizeRequestedLanguage(value.Language)
	if !languageValid {
		return request{}, errors.New("language 只支持 auto、zh、yue、en、ja 或 ko")
	}
	value.Language = normalizedLanguage
	if value.AudioPath == "" || value.ModelPath == "" {
		return request{}, errors.New("缺少音频路径或模型目录")
	}
	if decoded, err := hex.DecodeString(value.SourceAudioSHA256); err != nil || len(decoded) != sha256.Size || value.SourceAudioSHA256 != strings.ToLower(value.SourceAudioSHA256) {
		return request{}, errors.New("source_audio_sha256 必须是规范的 SHA-256")
	}
	return value, nil
}

func normalizeRequestedLanguage(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto", true
	case "zh", "yue", "en", "ja", "ko":
		return strings.ToLower(strings.TrimSpace(value)), true
	default:
		return "", false
	}
}

func normalizeObservedLanguage(value, requested string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(strings.TrimPrefix(value, "<|"), "|>")
	switch value {
	case "zh", "yue", "en", "ja", "ko":
		return value
	case "chinese", "cmn":
		return "zh"
	case "cantonese":
		return "yue"
	case "english":
		return "en"
	case "japanese":
		return "ja"
	case "korean":
		return "ko"
	}
	requested, validRequested := normalizeRequestedLanguage(requested)
	if !validRequested {
		requested = "auto"
	}
	if value == "" && requested != "auto" {
		return requested
	}
	return "unknown"
}

const modelIdentityCacheVersion = 1

// 适配器由 CLI 逐条语音消息启动一次，而 SenseVoice int8 模型有数百 MB：每次调用都
// 全量哈希，会让批量转写的耗时大致翻倍。因此对足够大的输入按 (路径, 大小, mtime)
// 缓存上一次的结果。
//
// 小输入不进缓存：哈希本来就便宜，而且文件系统的 mtime 粒度有限（Windows 的系统计时器
// 约 15.6 ms），连续写入可能拿到相同时间戳，此时缓存反而会给出过期身份。
var (
	modelIdentityCacheMinBytes int64 = 32 << 20
	modelIdentityCacheDir            = defaultModelIdentityCacheDir
)

func defaultModelIdentityCacheDir() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "v-local-cli-sensevoice"), nil
}

type modelIdentityInput struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	MTimeNS int64  `json:"mtime_ns"`
}

type modelIdentityRecord struct {
	Version  int                  `json:"version"`
	Identity string               `json:"identity"`
	Inputs   []modelIdentityInput `json:"inputs"`
}

func modelIdentityInputs(paths []string) ([]modelIdentityInput, int64, error) {
	values := make([]modelIdentityInput, 0, len(paths))
	total := int64(0)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, 0, err
		}
		if !info.Mode().IsRegular() {
			return nil, 0, errors.New("模型身份输入不是普通文件")
		}
		absolute, absErr := filepath.Abs(path)
		if absErr != nil {
			absolute = path
		}
		values = append(values, modelIdentityInput{
			Path: absolute, Size: info.Size(), MTimeNS: info.ModTime().UnixNano(),
		})
		total += info.Size()
	}
	return values, total, nil
}

func modelIdentityCachePath() (string, error) {
	directory, err := modelIdentityCacheDir()
	if err != nil || strings.TrimSpace(directory) == "" {
		return "", errors.New("模型身份缓存目录不可用")
	}
	return filepath.Join(directory, "model-identity-v1.json"), nil
}

// cachedModelIdentity 只在签名逐项一致时返回结果。任何读取或解析失败都退回全量计算，
// 缓存永远只是加速，不参与正确性。
func cachedModelIdentity(inputs []modelIdentityInput) string {
	path, err := modelIdentityCachePath()
	if err != nil {
		return ""
	}
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) > 64*1024 {
		return ""
	}
	var record modelIdentityRecord
	if json.Unmarshal(payload, &record) != nil || record.Version != modelIdentityCacheVersion ||
		record.Identity == "" || len(record.Inputs) != len(inputs) {
		return ""
	}
	for index := range inputs {
		if record.Inputs[index] != inputs[index] {
			return ""
		}
	}
	return record.Identity
}

func storeModelIdentity(inputs []modelIdentityInput, identity string) {
	path, err := modelIdentityCachePath()
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	payload, err := json.Marshal(modelIdentityRecord{
		Version: modelIdentityCacheVersion, Identity: identity, Inputs: inputs,
	})
	if err != nil {
		return
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".model-identity-*.tmp")
	if err != nil {
		return
	}
	temporary := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return
	}
	if _, err := file.Write(payload); err != nil {
		return
	}
	if err := file.Close(); err != nil {
		return
	}
	if os.Rename(temporary, path) == nil {
		remove = false
	}
}

func modelIdentity(paths ...string) (string, error) {
	inputs, total, err := modelIdentityInputs(paths)
	if err != nil {
		return "", err
	}
	cacheable := total >= modelIdentityCacheMinBytes
	if cacheable {
		if identity := cachedModelIdentity(inputs); identity != "" {
			return identity, nil
		}
	}
	identity, err := computeModelIdentity(paths)
	if err != nil {
		return "", err
	}
	if cacheable {
		storeModelIdentity(inputs, identity)
	}
	return identity, nil
}

func computeModelIdentity(paths []string) (string, error) {
	digest := sha256.New()
	_, _ = io.WriteString(digest, "v-local-cli-sensevoice/model-identity/v1\x00")
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			return "", errors.New("模型身份输入不是普通文件")
		}
		if _, err := fmt.Fprintf(digest, "%s\x00%d\x00", filepath.Base(path), info.Size()); err != nil {
			_ = file.Close()
			return "", err
		}
		_, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return fmt.Sprintf("sensevoice-int8@sha256:%x", digest.Sum(nil)), nil
}

func regularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("缺少普通文件：%s", filepath.Base(path))
	}
	return nil
}
