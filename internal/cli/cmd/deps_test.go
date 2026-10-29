package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// modulePrefix 与 go.mod 保持一致；所有内部包路径都以它开头。
const modulePrefix = "github.com/sy665/sycode/"

// rule 描述一条禁止的依赖边。from/to 是相对模块根的包路径前缀。
type rule struct {
	from string
	to   string
	why  string
}

// forbiddenEdges 是 docs/CODING-STANDARDS.md §2.2 的机器可执行版本。
//
// 说明：规则里出现的包尚未全部创建时，这一条自然不生效；
// 包一出现，规则立刻开始拦截——所以现在写下来不是空转。
var forbiddenEdges = []rule{
	{"internal/llm", "internal/cli", "provider 不认识 UI"},
	{"internal/llm", "internal/agentcore", "provider 不认识边界层"},
	{"internal/llm", "internal/appstate", "provider 不认识回合状态机"},
	{"internal/tools", "internal/llm", "工具不认识模型；schema 转换在 llm 侧"},
	{"internal/session", "internal/llm", "会话只存 json.RawMessage 与基础类型"},
	{"internal/appstate", "internal/cli", "状态机不认识 TUI"},
	{"internal/filesystem", "internal/", "基础设施包不依赖任何内部包"},
	{"internal/logging", "internal/", "日志是叶子包，不依赖任何内部包"},
}

// listedPackage 只取 go list 里用得到的字段。
type listedPackage struct {
	ImportPath string
	Imports    []string
}

// TestDependencyDirection 用 go list 读取真实 import 图，断言禁止边不存在。
//
// 用编译产物而不是正则扫源码：只有真实解析过的 import 才算数，
// 注释里的 "import xxx" 与字符串常量不会误报。
func TestDependencyDirection(t *testing.T) {
	root := moduleRoot(t)
	pkgs := listPackages(t, root)

	if len(pkgs) < 3 {
		t.Fatalf("包数量异常（%d），go list 可能没跑对", len(pkgs))
	}

	for _, pkg := range pkgs {
		from := trimModule(pkg.ImportPath)
		if from == "" {
			continue // 模块外（标准库、第三方）不看
		}
		for _, imp := range pkg.Imports {
			to := trimModule(imp)
			if to == "" {
				continue
			}
			for _, r := range forbiddenEdges {
				if !strings.HasPrefix(from, r.from) || !strings.HasPrefix(to, r.to) {
					continue
				}
				t.Errorf("依赖方向违规：%s → %s（%s）", from, to, r.why)
			}
		}
	}
}

// moduleRoot 找到仓库根，保证不论从哪个包目录跑测试，go list 都覆盖全模块。
func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Skipf("无法执行 go env GOMOD，跳过依赖方向检查：%v", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		t.Skip("当前目录不在 Go 模块内，跳过依赖方向检查")
	}
	return filepath.Dir(gomod)
}

// listPackages 在模块根执行 go list -json ./... 并解析成包列表。
func listPackages(t *testing.T, root string) []listedPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list 执行失败：%v\n%s", err, stderr.String())
	}

	var pkgs []listedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("解析 go list 输出失败：%v", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// trimModule 去掉模块前缀；非本模块包返回空串。
func trimModule(importPath string) string {
	if !strings.HasPrefix(importPath, modulePrefix) {
		return ""
	}
	return strings.TrimPrefix(importPath, modulePrefix)
}
