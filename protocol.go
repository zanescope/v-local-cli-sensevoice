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

type stagedModelBundle struct {
	Directory string
	Paths     []string
	Identity  string
}

func (bundle *stagedModelBundle) Close() error {
	if bundle == nil || bundle.Directory == "" {
		return nil
	}
	directory := bundle.Directory
	bundle.Directory = ""
	bundle.Paths = nil
	return os.RemoveAll(directory)
}

func stageModelBundle(paths ...string) (*stagedModelBundle, error) {
	if len(paths) == 0 {
		return nil, errors.New("模型身份输入为空")
	}
	directory, err := createPrivateModelStageDirectory()
	if err != nil {
		return nil, err
	}
	bundle := &stagedModelBundle{Directory: directory, Paths: make([]string, 0, len(paths))}
	fail := func(cause error) (*stagedModelBundle, error) {
		return nil, errors.Join(cause, bundle.Close())
	}
	digest := sha256.New()
	_, _ = io.WriteString(digest, "v-local-cli-sensevoice/model-identity/v1\x00")
	names := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		name := filepath.Base(path)
		if name == "." || name == ".." || name == "" || filepath.Clean(name) != name {
			return fail(errors.New("模型身份输入名称无效"))
		}
		if _, exists := names[name]; exists {
			return fail(errors.New("模型身份输入名称重复"))
		}
		names[name] = struct{}{}
		source, err := os.Open(path)
		if err != nil {
			return fail(err)
		}
		info, statErr := source.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			_ = source.Close()
			return fail(errors.New("模型身份输入不是普通文件"))
		}
		target := filepath.Join(directory, name)
		destination, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = source.Close()
			return fail(err)
		}
		if _, err := fmt.Fprintf(digest, "%s\x00%d\x00", name, info.Size()); err != nil {
			_ = source.Close()
			_ = destination.Close()
			return fail(err)
		}
		written, copyErr := io.Copy(io.MultiWriter(destination, digest), source)
		syncErr := destination.Sync()
		sourceCloseErr := source.Close()
		destinationCloseErr := destination.Close()
		if copyErr != nil || syncErr != nil || sourceCloseErr != nil || destinationCloseErr != nil {
			return fail(errors.Join(copyErr, syncErr, sourceCloseErr, destinationCloseErr))
		}
		if written != info.Size() {
			return fail(errors.New("模型身份输入在固定期间发生长度变化"))
		}
		bundle.Paths = append(bundle.Paths, target)
	}
	bundle.Identity = fmt.Sprintf("sensevoice-int8@sha256:%x", digest.Sum(nil))
	return bundle, nil
}

func modelIdentity(paths ...string) (string, error) {
	bundle, err := stageModelBundle(paths...)
	if err != nil {
		return "", err
	}
	defer func() { _ = bundle.Close() }()
	return bundle.Identity, nil
}

func regularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("缺少普通文件：%s", filepath.Base(path))
	}
	return nil
}
