package agentcore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sy665/sycode/internal/tools"
)

// waitRequest 等界面侧拿到一次询问。超时即失败：卡住说明请求根本没送到界面。
func waitRequest(t *testing.T, p *PermissionPrompt) tools.PermissionRequest {
	t.Helper()
	select {
	case req := <-p.Requests():
		return req
	case <-time.After(2 * time.Second):
		t.Fatal("超时：界面没有收到权限询问")
		return tools.PermissionRequest{}
	}
}

// waitDecision 等工具侧拿到答案。
func waitDecision(t *testing.T, ch <-chan tools.Decision) tools.Decision {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("超时：工具侧没有收到答案")
		return DecisionDeny
	}
}

func TestPermissionPromptAsksAndAnswers(t *testing.T) {
	p := NewPermissionPrompt()
	req := tools.PermissionRequest{Tool: "write_file", Op: "write", Path: "src/a.go", Detail: "写入文件内容"}

	decided := make(chan tools.Decision, 1)
	go func() {
		d, err := p.RequestPermission(context.Background(), req)
		if err != nil {
			t.Errorf("询问不该出错：%v", err)
		}
		decided <- d
	}()

	if got := waitRequest(t, p); got != req {
		t.Fatalf("界面收到的请求不符：期望 %+v，实际 %+v", req, got)
	}
	if !p.Answer(DecisionAllowOnce) {
		t.Fatal("应有待答请求")
	}
	if got := waitDecision(t, decided); got != DecisionAllowOnce {
		t.Fatalf("决定不符：期望 AllowOnce，实际 %v", got)
	}
}

func TestPermissionPromptRemembersSessionDecision(t *testing.T) {
	p := NewPermissionPrompt()
	ctx := context.Background()

	first := tools.PermissionRequest{Tool: "write_file", Op: "write", Path: "src/a.go"}
	decided := make(chan tools.Decision, 1)
	go func() {
		d, _ := p.RequestPermission(ctx, first)
		decided <- d
	}()
	waitRequest(t, p)
	p.Answer(DecisionAllowSession)
	waitDecision(t, decided)

	// 同目录的另一个文件：不该再打扰用户。
	second := tools.PermissionRequest{Tool: "write_file", Op: "write", Path: "src/b.go"}
	go func() {
		d, err := p.RequestPermission(ctx, second)
		if err != nil {
			t.Errorf("命中缓存不该出错：%v", err)
		}
		decided <- d
	}()
	if got := waitDecision(t, decided); got != DecisionAllowSession {
		t.Fatalf("同目录应直接放行，实际 %v", got)
	}
	select {
	case req := <-p.Requests():
		t.Fatalf("命中缓存后不该再发询问，却收到 %+v", req)
	default:
	}
}

func TestPermissionPromptAsksAgainForOtherDirectory(t *testing.T) {
	p := NewPermissionPrompt()
	ctx := context.Background()

	decided := make(chan tools.Decision, 1)
	go func() {
		d, _ := p.RequestPermission(ctx, tools.PermissionRequest{Tool: "write_file", Op: "write", Path: "src/a.go"})
		decided <- d
	}()
	waitRequest(t, p)
	p.Answer(DecisionAllowSession)
	waitDecision(t, decided)

	// 另一个目录：缓存键不同，必须再问一次。
	go func() {
		d, _ := p.RequestPermission(ctx, tools.PermissionRequest{Tool: "write_file", Op: "write", Path: "test/b.go"})
		decided <- d
	}()
	waitRequest(t, p)
	p.Answer(DecisionAllowOnce)
	if got := waitDecision(t, decided); got != DecisionAllowOnce {
		t.Fatalf("跨目录应重新询问，实际直接给了 %v", got)
	}
}

func TestPermissionPromptCancelWhileWaiting(t *testing.T) {
	p := NewPermissionPrompt()
	ctx, cancel := context.WithCancel(context.Background())

	failed := make(chan error, 1)
	go func() {
		_, err := p.RequestPermission(ctx, tools.PermissionRequest{Tool: "bash", Op: "exec", Path: "."})
		failed <- err
	}()

	waitRequest(t, p)
	cancel()

	select {
	case err := <-failed:
		// 必须是"取消"而不是"拒绝"：模型收到的文案不一样，一个是中断，一个是决定。
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("应返回取消错误，实际 %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时：取消后询问没有返回")
	}
	if p.Answer(DecisionAllowOnce) {
		t.Error("取消之后不该还留着待答请求")
	}
}

func TestPermissionPromptAnswerWithoutRequest(t *testing.T) {
	p := NewPermissionPrompt()
	if p.Answer(DecisionDeny) {
		t.Error("没有待答请求时 Answer 应返回 false")
	}
}

func TestPermissionPromptRejectsConcurrentRequests(t *testing.T) {
	p := NewPermissionPrompt()
	ctx := context.Background()

	decided := make(chan tools.Decision, 1)
	go func() {
		d, _ := p.RequestPermission(ctx, tools.PermissionRequest{Tool: "write_file", Op: "write", Path: "src/a.go"})
		decided <- d
	}()
	waitRequest(t, p)

	// 同一时刻的第二个询问：快速失败胜过静默覆盖等待槽（那会让第一个永远等下去）。
	failed := make(chan error, 1)
	go func() {
		_, err := p.RequestPermission(ctx, tools.PermissionRequest{Tool: "write_file", Op: "write", Path: "src/b.go"})
		failed <- err
	}()
	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("并发询问应当报错")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时：并发询问没有快速失败")
	}

	p.Answer(DecisionDeny)
	waitDecision(t, decided)
}
