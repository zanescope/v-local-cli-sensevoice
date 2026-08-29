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

func modelIdentity(paths ...string) (string, error) {
	// A content-bound identity cannot safely reuse a digest from path, size and
	// mtime alone: deployment tools can preserve all three while replacing the
	// bytes. Rehash both files on every short-lived adapter invocation.
	return computeModelIdentity(paths)
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
