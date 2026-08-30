//go:build cgo

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

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

func transcribe(input request) (response, error) {
	modelDirectory, err := filepath.Abs(input.ModelPath)
	if err != nil {
		return response{}, errors.New("模型目录无效")
	}
	modelPath := filepath.Join(modelDirectory, "model.int8.onnx")
	tokensPath := filepath.Join(modelDirectory, "tokens.txt")
	if err := regularFile(input.AudioPath); err != nil {
		return response{}, err
	}
	stagedModel, err := stageModelBundle(modelPath, tokensPath)
	if err != nil {
		return response{}, errors.New("无法固定 SenseVoice 模型内容")
	}
	defer func() { _ = stagedModel.Close() }()

	wave := sherpa.ReadWave(input.AudioPath)
	if wave == nil || wave.SampleRate != 16000 || len(wave.Samples) == 0 {
		return response{}, errors.New("无法读取有效的 16 kHz WAV")
	}
	config := sherpa.OfflineRecognizerConfig{}
	config.FeatConfig.SampleRate = 16000
	config.FeatConfig.FeatureDim = 80
	config.ModelConfig.SenseVoice.Model = stagedModel.Paths[0]
	config.ModelConfig.SenseVoice.Language = input.Language
	config.ModelConfig.SenseVoice.UseInverseTextNormalization = 1
	config.ModelConfig.Tokens = stagedModel.Paths[1]
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
	language := normalizeObservedLanguage(result.Lang, input.Language)
	return response{
		Protocol: protocolName, Transcript: text, Engine: "sherpa-onnx-sensevoice",
		Model: stagedModel.Identity, Language: language, NetworkUsed: false,
	}, nil
}

func fail(err error, code int) {
	fmt.Fprintf(os.Stderr, "v-local-cli-sensevoice: %v\n", err)
	os.Exit(code)
}
