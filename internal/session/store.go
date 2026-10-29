package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// transcriptFile 是会话正文的文件名。故意不用 `.json` 后缀：
	// 它是 JSONL，用编辑器打开时不该被当成一个合法 JSON 对象。
	transcriptFile = "transcript_events.jsonl"

	// maxEventBytes 是单条事件的读取上限。
	//
	// 8MiB 远大于任何真实回合（模型一屏正文也就几十 KB），
	// 它的作用是在文件被外部写坏时挡住"一行吃掉整个内存"。
	maxEventBytes = 8 << 20

	// readBufferSize 是逐行读的缓冲大小。行本身可以比它长，ReadBytes 会自动扩容。
	readBufferSize = 64 << 10

	// titleRunes 是列表里标题的截断长度。
	titleRunes = 40
)

// Summary 是一个会话的摘要，供列表与"恢复最近一次"使用。
type Summary struct {
	ID      string
	Started time.Time
	Updated time.Time
	// Turns 是用户发起的回合数（user_message 的条数）。
	Turns int
	// Title 是第一条用户输入的第一行，供列表辨认。
	Title string
	// WorkDir 来自 session_started；老文件缺这条事件时为空。
	WorkDir string
	// Events 是有效事件数，Corrupt 是读不出来的行数。
	Events  int
	Corrupt int
}

// Loaded 是一个会话的完整读回结果。
type Loaded struct {
	Summary
	Events []Event
}

// Store 是一个工作目录的会话库（`~/.sycode/sessions/<命名空间>`）。
//
// 按工作目录分命名空间：同一个项目的历史聚在一起，不同项目互不可见。
type Store struct {
	dir     string
	workDir string
}

// NewStore 只算路径，不建目录：只读操作（列表、恢复）不该在磁盘上留下空目录。
func NewStore(workingDir string) (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("执行失败：无法确定用户目录：%w", err)
	}
	return &Store{
		dir:     filepath.Join(home, ".sycode", "sessions", namespaceDirName(workingDir)),
		workDir: workingDir,
	}, nil
}

// Session 是一个已建立的会话：知道文件在哪，也知道下一条事件是几号。
//
// 不持有文件句柄：每次追加打开-写入-关闭。追加的频率是"一条用户输入一次"，
// 开销可以忽略，换来的是不必管理句柄生命周期，也不会因为忘记 Close 而丢缓冲。
type Session struct {
	ID   string
	dir  string
	file string

	mu   sync.Mutex
	next uint64
}

// Create 新建一个会话并写下 session_started。
func (s *Store) Create() (*Session, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(s.dir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("执行失败：无法创建会话目录 %q：%w", dir, err)
	}

	sess := &Session{ID: id, dir: dir, file: filepath.Join(dir, transcriptFile), next: 1}
	ev, err := NewEvent(KindSessionStarted, SessionStarted{WorkDir: s.workDir})
	if err != nil {
		return nil, err
	}
	if err := sess.append(ev); err != nil {
		return nil, err
	}
	return sess, nil
}

// Append 追加一条事件，序号由会话递增分配。
//
// 调用方只负责给出 Kind 与载荷；Seq 与零值 TS 都在这里补，
// 否则"谁该赋序号"会有两种说法，早晚出现重复序号。
func (s *Store) Append(sess *Session, ev Event) error {
	if sess == nil {
		return errors.New("参数错误：没有会话，无法追加事件")
	}
	return sess.append(ev)
}

// append 是整个包唯一的写入路径。
func (s *Session) append(ev Event) error {
	if ev.Kind == "" {
		return errors.New("参数错误：事件类型为空")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ev.Seq = s.next
	if ev.TS.IsZero() {
		ev.TS = time.Now()
	}
	if len(ev.Data) == 0 {
		ev.Data = json.RawMessage("{}")
	}

	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("执行失败：无法编码事件 %q：%w", ev.Kind, err)
	}
	line = append(line, '\n')

	file, err := os.OpenFile(s.file, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("执行失败：无法打开会话文件 %q：%w", s.file, err)
	}
	defer file.Close()

	// 上一次进程可能死在半行上：先补一个换行，别让新事件拼在半截 JSON 后面
	// （那会让"一条坏行"变成"两条坏行"）。
	if err := ensureTrailingNewline(file); err != nil {
		return err
	}
	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("执行失败：写入会话文件 %q 失败：%w", s.file, err)
	}

	s.next++
	return nil
}

// Load 读回一个会话的全部事件。
func (s *Store) Load(id string) (*Loaded, error) {
	if id == "" {
		return nil, errors.New("参数错误：会话 ID 为空")
	}
	path := filepath.Join(s.dir, id, transcriptFile)

	events, corrupt, err := readEvents(path)
	if err != nil {
		return nil, err
	}
	return &Loaded{Summary: summarize(id, events, corrupt), Events: events}, nil
}

// List 列出本工作目录的全部会话，最近更新的在前。
//
// 靠扫文件而不是 index.json：两处维护索引必然会不一致，而会话文件本来就不大。
// 单个会话读不出来时跳过它——一个坏文件不该让"列出全部"整个失败。
func (s *Store) List() ([]Summary, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("执行失败：无法读取会话目录 %q：%w", s.dir, err)
	}

	summaries := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		events, corrupt, err := readEvents(filepath.Join(s.dir, entry.Name(), transcriptFile))
		if err != nil || len(events) == 0 {
			continue
		}
		summaries = append(summaries, summarize(entry.Name(), events, corrupt))
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Updated.After(summaries[j].Updated)
	})
	return summaries, nil
}

// Latest 取最近更新的会话；没有会话时第二个返回值为 false。
func (s *Store) Latest() (Summary, bool, error) {
	list, err := s.List()
	if err != nil {
		return Summary{}, false, err
	}
	if len(list) == 0 {
		return Summary{}, false, nil
	}
	return list[0], true, nil
}

// readEvents 逐行读事件，返回有效事件与坏行数。
//
// 坏行只计数不报错：会话文件是纯追加的，进程被杀、磁盘写满都会留下半行，
// 而"读到一半就报错"等于让用户永远打不开这个会话。
func readEvents(path string) ([]Event, int, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, 0, fmt.Errorf("执行失败：会话文件 %q 不存在", path)
		}
		return nil, 0, fmt.Errorf("执行失败：无法打开会话文件 %q：%w", path, err)
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, readBufferSize)
	var (
		events  []Event
		corrupt int
	)
	for {
		line, readErr := reader.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var ev Event
			switch {
			case len(trimmed) > maxEventBytes:
				corrupt++
			case json.Unmarshal(trimmed, &ev) != nil || ev.Kind == "":
				corrupt++
			case !validEvent(&ev):
				corrupt++
			default:
				events = append(events, ev)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, 0, fmt.Errorf("执行失败：读取会话文件 %q 时出错：%w", path, readErr)
		}
	}
	return events, corrupt, nil
}

// validEvent 检查一条事件的最低限度：序号不能是 0（写入时从 1 开始）。
//
// 只看这一点就够：Kind 已在上层判过，Data 允许为空对象。
// 序号为 0 通常意味着这行是别的东西被写进了文件，而不是我们的半行。
func validEvent(ev *Event) bool { return ev.Seq > 0 }

// summarize 从事件流算出摘要。
func summarize(id string, events []Event, corrupt int) Summary {
	sum := Summary{ID: id, Events: len(events), Corrupt: corrupt}
	if len(events) > 0 {
		sum.Started = events[0].TS
		sum.Updated = events[0].TS
	}

	for _, ev := range events {
		if ev.TS.After(sum.Updated) {
			sum.Updated = ev.TS
		}
		switch ev.Kind {
		case KindSessionStarted:
			var payload SessionStarted
			if ev.Decode(&payload) == nil {
				sum.WorkDir = payload.WorkDir
			}
		case KindUserMessage:
			sum.Turns++
			if sum.Title == "" {
				var payload UserMessage
				if ev.Decode(&payload) == nil {
					sum.Title = shortenTitle(payload.Text)
				}
			}
		}
	}
	return sum
}

// shortenTitle 取第一条用户输入的首行，截到 titleRunes 个字符。
//
// 按 rune 截而不是按字节：中文一个字三字节，切一半会留下替换符。
func shortenTitle(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= titleRunes {
		return string(runes)
	}
	return string(runes[:titleRunes]) + "…"
}

// ensureTrailingNewline 在文件末尾缺换行时补一个。
func ensureTrailingNewline(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("执行失败：无法读取会话文件信息：%w", err)
	}
	if info.Size() == 0 {
		return nil
	}

	if _, err := file.Seek(-1, io.SeekEnd); err != nil {
		return fmt.Errorf("执行失败：无法定位会话文件末尾：%w", err)
	}
	last := make([]byte, 1)
	if _, err := io.ReadFull(file, last); err != nil {
		return fmt.Errorf("执行失败：无法读取会话文件末尾：%w", err)
	}
	if last[0] == '\n' {
		return nil
	}
	// O_APPEND 保证写入落在末尾，不受上面 Seek 的影响。
	if _, err := file.Write([]byte{'\n'}); err != nil {
		return fmt.Errorf("执行失败：无法修正会话文件末尾：%w", err)
	}
	return nil
}

// newSessionID 生成 `20261029-221530-a1b2` 形式的 ID：可读、可排序、同秒不撞。
func newSessionID() (string, error) {
	suffix := make([]byte, 2)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("执行失败：无法生成会话 ID：%w", err)
	}
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(suffix), nil
}
