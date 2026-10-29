package repl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sy665/sycode/internal/llm"
)

func TestParseFormat(t *testing.T) {
	for _, raw := range []string{"", "text"} {
		if got, err := ParseFormat(raw); err != nil || got != FormatText {
			t.Errorf("ParseFormat(%q) = %q, %v；期望 text", raw, got, err)
		}
	}
	if got, err := ParseFormat("json"); err != nil || got != FormatJSON {
		t.Errorf("json 应被接受，实际 %q, %v", got, err)
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("非法格式应报错")
	} else if !strings.Contains(err.Error(), "text / json") {
		t.Errorf("错误应给出可选值：%v", err)
	}
}

func TestRunHeadlessStreamsText(t *testing.T) {
	core := &fakeCore{events: []llm.StreamEvent{
		{Type: llm.EventText, Text: "第一段，"},
		{Type: llm.EventText, Text: "第二段。"},
		{Type: llm.EventDone},
	}}
	var out bytes.Buffer

	result, err := RunHeadless(context.Background(), core, HeadlessOptions{
		Prompt: "你好", Format: FormatText, Out: &out,
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if got := out.String(); got != "第一段，第二段。\n" {
		t.Errorf("输出不符：%q", got)
	}
	if result.Text != "第一段，第二段。" {
		t.Errorf("结果里的正文不符：%q", result.Text)
	}
}

func TestRunHeadlessJSONCarriesToolsAndUsage(t *testing.T) {
	call := &llm.ToolCall{ID: "c1", Name: "read_file"}
	core := &fakeCore{events: []llm.StreamEvent{
		{Type: llm.EventText, Text: "看完了"},
		{Type: llm.EventToolResult, Tool: call, Text: "read_file 完成（42 B）\n+a\n-b"},
		{Type: llm.EventUsage, Usage: &llm.Usage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150}},
		{Type: llm.EventDone},
	}}
	var out bytes.Buffer

	_, err := RunHeadless(context.Background(), core, HeadlessOptions{
		Prompt: "看看文件", Format: FormatJSON, Out: &out,
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}

	var got HeadlessResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("输出不是合法 JSON：%v\n%s", err, out.String())
	}
	if got.Text != "看完了" {
		t.Errorf("正文不符：%q", got.Text)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "read_file" || !got.Tools[0].OK {
		t.Fatalf("工具摘要不符：%+v", got.Tools)
	}
	// 摘要只要一行：展示块是给人当场看的，脚本不需要。
	if strings.Contains(got.Tools[0].Summary, "\n") || got.Tools[0].Summary != "read_file 完成（42 B）" {
		t.Errorf("工具摘要应只取首行：%q", got.Tools[0].Summary)
	}
	if got.Usage == nil || got.Usage.TotalTokens != 150 {
		t.Errorf("用量不符：%+v", got.Usage)
	}
}

func TestRunHeadlessErrorKeepsPartialText(t *testing.T) {
	core := &fakeCore{events: []llm.StreamEvent{
		{Type: llm.EventText, Text: "说到一半"},
		{Type: llm.EventError, Err: errors.New("执行失败：模型服务返回 500")},
	}}
	var out bytes.Buffer

	result, err := RunHeadless(context.Background(), core, HeadlessOptions{
		Prompt: "问", Format: FormatText, Out: &out,
	})
	if err == nil {
		t.Fatal("应把错误报到上层")
	}
	// 半句话也好过空输出：脚本至少能看到模型说到了哪里。
	if result.Text != "说到一半" {
		t.Errorf("结果应保留已生成的正文：%q", result.Text)
	}
	if !strings.Contains(out.String(), "说到一半") {
		t.Errorf("输出应包含已生成的正文：%q", out.String())
	}
}

func TestRunHeadlessJSONOnError(t *testing.T) {
	core := &fakeCore{events: []llm.StreamEvent{
		{Type: llm.EventText, Text: "半句"},
		{Type: llm.EventError, Err: errors.New("执行失败：网络中断")},
	}}
	var out bytes.Buffer

	if _, err := RunHeadless(context.Background(), core, HeadlessOptions{
		Prompt: "问", Format: FormatJSON, Out: &out,
	}); err == nil {
		t.Fatal("应返回错误")
	}

	// 出错也要给出结构化的部分结果：调用方既要知道失败，也要拿到已经算出来的正文。
	var got HeadlessResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("出错时也应输出合法 JSON：%v\n%s", err, out.String())
	}
	if got.Text != "半句" {
		t.Errorf("正文不符：%q", got.Text)
	}
}

func TestRunHeadlessRejectsEmptyPrompt(t *testing.T) {
	var out bytes.Buffer
	if _, err := RunHeadless(context.Background(), &fakeCore{}, HeadlessOptions{
		Prompt: "   ", Out: &out,
	}); err == nil {
		t.Fatal("空提示词应报错")
	}
}

func TestRunHeadlessWithoutCore(t *testing.T) {
	var out bytes.Buffer
	_, err := RunHeadless(context.Background(), nil, HeadlessOptions{Prompt: "问", Out: &out})
	if err == nil {
		t.Fatal("没有引擎应报错")
	}
	if !strings.Contains(err.Error(), "没有可用的模型客户端") {
		t.Errorf("错误应指出缺什么：%v", err)
	}
}
