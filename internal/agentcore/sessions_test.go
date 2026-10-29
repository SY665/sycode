package agentcore

import (
	"testing"

	"github.com/sy665/sycode/internal/session"
)

// newStoreWithSession 在临时 home 里建库并写一轮对话。
func newStoreWithSession(t *testing.T, turns int) (*session.Store, *session.Session) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("建会话库失败：%v", err)
	}
	sess, err := store.Create()
	if err != nil {
		t.Fatalf("建会话失败：%v", err)
	}
	for i := 0; i < turns; i++ {
		ev, err := session.NewEvent(session.KindUserMessage, session.UserMessage{Text: "第几句话"})
		if err != nil {
			t.Fatalf("建事件失败：%v", err)
		}
		if err := store.Append(sess, ev); err != nil {
			t.Fatalf("追加失败：%v", err)
		}
	}
	return store, sess
}

func TestSessionsWithoutStore(t *testing.T) {
	// 没落盘是正常装配（测试、预览）：列会话应给空列表，而不是错误。
	core := NewWithSession(nil, nil, nil, nil, "", nil, nil)
	list, err := core.Sessions()
	if err != nil {
		t.Fatalf("没有会话库不该报错：%v", err)
	}
	if len(list) != 0 {
		t.Errorf("应是空列表，实际 %d 条", len(list))
	}
}

func TestSessionsSkipsEmptyOnes(t *testing.T) {
	store, sess := newStoreWithSession(t, 2)

	// 再建一个一句话没说的会话：它在列表里只会让人以为有东西可恢复。
	if _, err := store.Create(); err != nil {
		t.Fatalf("建空会话失败：%v", err)
	}

	core := NewWithSession(nil, nil, nil, nil, "", store, sess)
	list, err := core.Sessions()
	if err != nil {
		t.Fatalf("列会话失败：%v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应只剩 1 个非空会话，实际 %d：%+v", len(list), list)
	}
	if list[0].ID != sess.ID {
		t.Errorf("ID 不符：%q", list[0].ID)
	}
	if list[0].Turns != 2 {
		t.Errorf("轮数不符：%d", list[0].Turns)
	}
	if list[0].Title != "第几句话" {
		t.Errorf("标题应取第一条用户输入：%q", list[0].Title)
	}
}
