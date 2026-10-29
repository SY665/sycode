package session

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newTestStore 把 home 指到临时目录：会话默认落在 ~/.sycode，不能写脏真实 home。
func newTestStore(t *testing.T, workDir string) *Store {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	store, err := NewStore(workDir)
	if err != nil {
		t.Fatalf("建 Store 失败：%v", err)
	}
	return store
}

func mustCreate(t *testing.T, store *Store) *Session {
	t.Helper()
	sess, err := store.Create()
	if err != nil {
		t.Fatalf("建会话失败：%v", err)
	}
	return sess
}

func mustAppend(t *testing.T, store *Store, sess *Session, kind EventKind, payload any) {
	t.Helper()
	ev, err := NewEvent(kind, payload)
	if err != nil {
		t.Fatalf("建事件失败：%v", err)
	}
	if err := store.Append(sess, ev); err != nil {
		t.Fatalf("追加事件失败：%v", err)
	}
}

func TestNamespaceDirNameDistinguishesPaths(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()

	// 不同目录必须落到不同命名空间，哪怕最后一段同名（临时目录下面的 app 子目录）。
	if namespaceDirName(filepath.Join(a, "app")) == namespaceDirName(filepath.Join(b, "app")) {
		t.Error("不同路径应得到不同命名空间")
	}

	// 同一目录的相对写法与绝对写法必须一致，否则"从子目录启动"会看不到历史。
	abs := filepath.Join(a, "app")
	if got, want := namespaceDirName(abs), namespaceDirName(filepath.Join(a, ".", "app")); got != want {
		t.Errorf("同一目录的不同写法应一致：%q vs %q", got, want)
	}

	if runtime.GOOS == "windows" {
		if got, want := namespaceDirName(abs), namespaceDirName(strings.ToUpper(abs)); got != want {
			t.Errorf("Windows 上大小写不同应视为同一目录：%q vs %q", got, want)
		}
	}
}

func TestNamespaceDirNameIsReadable(t *testing.T) {
	name := namespaceDirName(filepath.Join(t.TempDir(), "我的项目"))

	// 形状必须是「可读前缀-10 位哈希」，且只含文件名安全字符：
	// 少了哈希会撞车，含中文或分隔符会在某些文件系统上直接建不出目录。
	if !regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}-[0-9a-f]{10}$`).MatchString(name) {
		t.Errorf("命名空间形状不符：%q", name)
	}
	// 全中文的目录名会被整体替换掉，此时退回固定前缀，而不是留一个空名字。
	if !strings.HasPrefix(name, "root-") {
		t.Errorf("无可读字符时应退回 root 前缀：%q", name)
	}

	readable := namespaceDirName(filepath.Join(t.TempDir(), "my-app"))
	if !strings.HasPrefix(readable, "my-app-") {
		t.Errorf("可读字符应保留在哈希之前：%q", readable)
	}
}

func TestCreateWritesSessionStarted(t *testing.T) {
	store := newTestStore(t, `C:\work\app`)
	sess := mustCreate(t, store)

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if len(loaded.Events) != 1 {
		t.Fatalf("新会话应只有 1 条事件，实际 %d", len(loaded.Events))
	}
	first := loaded.Events[0]
	if first.Kind != KindSessionStarted {
		t.Errorf("首条应是 session_started，实际 %q", first.Kind)
	}
	if first.Seq != 1 {
		t.Errorf("序号应从 1 开始，实际 %d", first.Seq)
	}

	var payload SessionStarted
	if err := first.Decode(&payload); err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if payload.WorkDir != `C:\work\app` {
		t.Errorf("工作目录不符：%q", payload.WorkDir)
	}
}

func TestAppendAssignsIncreasingSequence(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	sess := mustCreate(t, store)

	mustAppend(t, store, sess, KindUserMessage, UserMessage{Text: "第一句"})
	mustAppend(t, store, sess, KindUserMessage, UserMessage{Text: "第二句"})

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if len(loaded.Events) != 3 {
		t.Fatalf("应有 3 条事件，实际 %d", len(loaded.Events))
	}
	for i, ev := range loaded.Events {
		if want := uint64(i + 1); ev.Seq != want {
			t.Errorf("第 %d 条的序号应为 %d，实际 %d", i, want, ev.Seq)
		}
	}
}

func TestLoadRoundTripsPayloads(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	sess := mustCreate(t, store)

	turn := AssistantTurn{
		Text: "改好了",
		Tools: []ToolRecord{
			{Name: "edit_file", OK: true, Summary: "edit_file 完成（2 B）"},
			{Name: "bash", OK: false, Summary: "bash 失败：退出码 1"},
		},
	}
	mustAppend(t, store, sess, KindUserMessage, UserMessage{Text: "改一下 a.go"})
	mustAppend(t, store, sess, KindAssistantTurn, turn)

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}

	var user UserMessage
	if err := loaded.Events[1].Decode(&user); err != nil {
		t.Fatalf("解码用户消息失败：%v", err)
	}
	if user.Text != "改一下 a.go" {
		t.Errorf("用户消息不符：%q", user.Text)
	}

	var got AssistantTurn
	if err := loaded.Events[2].Decode(&got); err != nil {
		t.Fatalf("解码回合失败：%v", err)
	}
	if got.Text != turn.Text {
		t.Errorf("正文不符：%q", got.Text)
	}
	if len(got.Tools) != 2 || got.Tools[1].Name != "bash" || got.Tools[1].OK {
		t.Errorf("工具记录不符：%+v", got.Tools)
	}
}

func TestLoadSkipsCorruptLines(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	sess := mustCreate(t, store)
	mustAppend(t, store, sess, KindUserMessage, UserMessage{Text: "好的"})

	// 手工造三种坏行：半截 JSON、空行、序号为 0 的"不是我们写的"对象。
	path := filepath.Join(store.dir, sess.ID, transcriptFile)
	bad := "{\"seq\":9,\"kind\":\"user_mess\n\n\n{\"seq\":0,\"kind\":\"user_message\",\"data\":{}}\n"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("打开会话文件失败：%v", err)
	}
	if _, err := file.WriteString(bad); err != nil {
		t.Fatalf("写坏行失败：%v", err)
	}
	file.Close()

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("坏行不该让读取失败：%v", err)
	}
	if len(loaded.Events) != 2 {
		t.Fatalf("应读回 2 条有效事件，实际 %d：%+v", len(loaded.Events), loaded.Events)
	}
	if loaded.Corrupt != 2 {
		t.Errorf("应记 2 条坏行（半截 JSON 与 seq=0），空行不算，实际 %d", loaded.Corrupt)
	}
}

func TestAppendAfterTruncatedTail(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	sess := mustCreate(t, store)

	// 模拟进程死在半行上：文件末尾没有换行。
	path := filepath.Join(store.dir, sess.ID, transcriptFile)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("打开会话文件失败：%v", err)
	}
	if _, err := file.WriteString("{\"seq\":2,\"kind\":\"user_mess"); err != nil {
		t.Fatalf("写半行失败：%v", err)
	}
	file.Close()

	mustAppend(t, store, sess, KindUserMessage, UserMessage{Text: "接着说"})

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	// 半截那行算一条坏行，新事件必须完整可读——不能拼在半截 JSON 后面。
	if len(loaded.Events) != 2 {
		t.Fatalf("应有 2 条有效事件（started + 新消息），实际 %d：%+v", len(loaded.Events), loaded.Events)
	}
	if loaded.Corrupt != 1 {
		t.Errorf("半截行应记 1 条坏行，实际 %d", loaded.Corrupt)
	}
	var last UserMessage
	if err := loaded.Events[1].Decode(&last); err != nil || last.Text != "接着说" {
		t.Errorf("新事件应完好：%q（err=%v）", last.Text, err)
	}
}

func TestListSummarizesAndOrders(t *testing.T) {
	store := newTestStore(t, t.TempDir())

	older := mustCreate(t, store)
	mustAppend(t, store, older, KindUserMessage, UserMessage{Text: "第一件事\n第二行不要"})

	// 让两个会话的 Updated 明确不同：文件系统时间精度有限，靠内容区分不可靠。
	newer := mustCreate(t, store)
	mustAppend(t, store, newer, KindUserMessage, UserMessage{Text: "第二件事"})
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(store.dir, newer.ID, transcriptFile), future, future); err != nil {
		t.Fatalf("改时间失败：%v", err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("列会话失败：%v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 个会话，实际 %d", len(list))
	}
	if list[0].ID != newer.ID {
		t.Errorf("最近更新的应排第一，实际 %q", list[0].ID)
	}
	if list[1].Turns != 1 {
		t.Errorf("Turns 应为 1，实际 %d", list[1].Turns)
	}
	if list[1].Title != "第一件事" {
		t.Errorf("标题应只取首行，实际 %q", list[1].Title)
	}

	latest, ok, err := store.Latest()
	if err != nil || !ok {
		t.Fatalf("Latest 应有结果：ok=%v err=%v", ok, err)
	}
	if latest.ID != newer.ID {
		t.Errorf("Latest 应取最新的，实际 %q", latest.ID)
	}
}

func TestListWithoutSessions(t *testing.T) {
	store := newTestStore(t, t.TempDir())

	list, err := store.List()
	if err != nil {
		t.Fatalf("没有会话不该报错：%v", err)
	}
	if len(list) != 0 {
		t.Errorf("应是空列表，实际 %d 条", len(list))
	}
	if _, ok, err := store.Latest(); ok || err != nil {
		t.Errorf("没有会话时 Latest 应返回 false 且无错：ok=%v err=%v", ok, err)
	}
}

func TestLoadMissingSession(t *testing.T) {
	store := newTestStore(t, t.TempDir())

	if _, err := store.Load("不存在的会话"); err == nil {
		t.Fatal("读不存在的会话应报错")
	}
}

func TestShortenTitle(t *testing.T) {
	if got := shortenTitle("  短标题  "); got != "短标题" {
		t.Errorf("应去掉首尾空白：%q", got)
	}
	long := strings.Repeat("很", titleRunes+5)
	got := shortenTitle(long)
	if runes := []rune(got); len(runes) != titleRunes+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("长标题应截到 %d 个字加省略号：%q（%d 字）", titleRunes, got, len(runes))
	}
}
