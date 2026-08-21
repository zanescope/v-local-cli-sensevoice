package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

const protocolName = "v-local-cli-asr/1"
const maxRequestBytes = 64 * 1024

var version = "0.1.0-dev.0"

type request struct {
	Protocol  string `json:"protocol"`
	Action    string `json:"action"`
	AudioPath string `json:"audio_path"`
	// SourceAudioSHA256 是原始语音（SILK 解码前）的摘要，是调用方的溯源标记，
	// 而非输入 WAV 的摘要。适配器只拿到解码后的 WAV，无法据此校验，故仅接收不使用。
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

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Fprintln(os.Stdout, version)
		return
	}
	if len(os.Args) != 1 {
		fail(errors.New("适配器不接受命令行参数"), 2)
	}
	input, err := decodeRequest(os.Stdin)
	if err != nil {
		fail(err, 2)
	}
	result, err := transcribe(input)
	if err != nil {
		fail(err, 1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fail(errors.New("无法编码适配器响应"), 1)
	}
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
	value.Language = normalizeLanguage(value.Language)
	if value.AudioPath == "" || value.ModelPath == "" {
		return request{}, errors.New("缺少音频路径或模型目录")
	}
	return value, nil
}

func normalizeLanguage(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto"
	case "zh", "yue", "en", "ja", "ko":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "auto"
	}
}

func transcribe(input request) (response, error) {
	modelDirectory, err := filepath.Abs(input.ModelPath)
	if err != nil {
		return response{}, errors.New("模型目录无效")
	}
	modelPath := filepath.Join(modelDirectory, "model.int8.onnx")
	tokensPath := filepath.Join(modelDirectory, "tokens.txt")
	if err := regularFile(modelPath); err != nil {
		return response{}, err
	}
	if err := regularFile(tokensPath); err != nil {
		return response{}, err
	}
	if err := regularFile(input.AudioPath); err != nil {
		return response{}, err
	}

	wave := sherpa.ReadWave(input.AudioPath)
	if wave == nil || wave.SampleRate != 16000 || len(wave.Samples) == 0 {
		return response{}, errors.New("无法读取有效的 16 kHz WAV")
	}
	config := sherpa.OfflineRecognizerConfig{}
	config.FeatConfig.SampleRate = 16000
	config.FeatConfig.FeatureDim = 80
	config.ModelConfig.SenseVoice.Model = modelPath
	config.ModelConfig.SenseVoice.Language = input.Language
	config.ModelConfig.SenseVoice.UseInverseTextNormalization = 1
	config.ModelConfig.Tokens = tokensPath
	config.ModelConfig.NumThreads = max(1, min(4, runtime.NumCPU()/2))
	config.ModelConfig.Provider = "cpu"
	config.DecodingMethod = "greedy_search"

	recognizer := sherpa.NewOfflineRecognizer(&config)
	if recognizer == nil {
		return response{}, errors.New("无法初始化 SenseVoice 识别器")
	}
	defer sherpa.DeleteOfflineRecognizer(recognizer)
	stream := sherpa.NewOfflineStream(recognizer)
	if stream == nil {
		return response{}, errors.New("无法创建 SenseVoice 音频流")
	}
	defer sherpa.DeleteOfflineStream(stream)
	stream.AcceptWaveform(wave.SampleRate, wave.Samples)
	recognizer.Decode(stream)
	result := stream.GetResult()
	text := strings.TrimSpace(result.Text)
	if text == "" {
		return response{}, errors.New("SenseVoice 没有返回文字")
	}
	language := strings.TrimSpace(result.Lang)
	if language == "" {
		language = input.Language
	}
	return response{
		Protocol: protocolName, Transcript: text, Engine: "sherpa-onnx-sensevoice",
		Model: "sensevoice-small-int8", Language: language, NetworkUsed: false,
	}, nil
}

func regularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("缺少普通文件：%s", filepath.Base(path))
	}
	return nil
}

func fail(err error, code int) {
	fmt.Fprintf(os.Stderr, "v-local-cli-sensevoice: %v\n", err)
	os.Exit(code)
}
