package main

import (
	"strings"
	"testing"
)

func TestDecodeRequest(t *testing.T) {
	value, err := decodeRequest(strings.NewReader(`{"protocol":"v-local-cli-asr/1","action":"transcribe","audio_path":"sample.wav","source_audio_sha256":"digest","sample_rate":16000,"channels":1,"language":"zh","model_path":"model"}`))
	if err != nil || value.Language != "zh" {
		t.Fatalf("协议请求解析失败：value=%+v err=%v", value, err)
	}
	if _, err := decodeRequest(strings.NewReader(`{"protocol":"v-local-cli-asr/1","action":"transcribe","audio_path":"sample.wav","sample_rate":24000,"channels":1,"model_path":"model"}`)); err == nil {
		t.Fatal("不应接受错误采样率")
	}
}

func TestNormalizeLanguage(t *testing.T) {
	if normalizeLanguage("yue") != "yue" || normalizeLanguage("invalid") != "auto" {
		t.Fatal("语言归一化错误")
	}
}
