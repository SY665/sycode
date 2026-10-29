# 实施记录：P2 单元 2.3 启动接线

- 日期：2026-10-29
- 结论：完成，质量门全绿；真实二进制逐场景验证通过（含切供应商）；顺手修掉 `Resolve` 的默认模型回退 bug

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `cmd/main.go` | 114 | 新增 `resolveModel()`（§1 的 2–5 步）；`run()` 把它注入 `cmd.Options.ResolveModel` |
| `internal/cli/cmd/root.go` | 239 | `Options` 增 `ResolveModel`，`StartREPL` 改收 `(model string)`；`runREPL` 先解析再进界面；私有 `resolveModel` 兜 nil |
| `internal/cli/cmd/root_test.go` | 243 | +3 组：模型名传到界面、解析失败不进界面（退出码 1 + 文案含变量名）、`--version` 不触发解析 |
| `internal/config/config.go` | 158 | `Resolve` 的默认模型回退修正 |
| `internal/providers/registry.go` | 168 | 新增 `FirstModel` |
| `*_test.go` | — | +2 组：只指定供应商 → 第一个模型；`FirstModel` 命中/未命中 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| 解析做成**延迟调用**（`Options.ResolveModel`）而不是在 `run()` 里提前跑完 | 版本、帮助不需要密钥。提前解析会让新用户敲的第一条 `sycode --version` 撞上"缺少密钥"，卡在他还没机会配置的时候 |
| 缺密钥**退出 1**，不按原计划的 2 | 2 是"用法错误"（ARCHITECTURE §411），而 `run()` 对 2 会印"参数错误：… 用法见 sycode --help"——缺密钥印成参数错误只会误导。已同步改 CODE-DESIGN §1 |
| 解析失败错误**原样上抛**，不包一层 | `config` 的文案已带"该设哪个变量"，再包就是"错误：无法确定模型：缺少密钥：…"的叠词（§13 不许改写下层文案） |
| `Env`/`Deps` 只传模型名，不传 key/base_url | P2.4 接 `llm` 才需要它们；现在传过去只会是没人读的字段。届时按需扩成结构体，改动点集中在一处 |
| `StartREPL` 改成收 `model string` | 让测试能断言"模型真的传到了界面"；同时保留"替换整个界面"的隔离能力（否则 `go test` 在交互终端里会挂住） |

## 3. 抓到的 bug：默认模型回退

真实二进制验证"把 `active_provider` 改成 deepseek"时撞到：

```
错误：模型 "gpt-4o-mini" 不属于供应商 "deepseek"。请检查配置中的 active_model
```

`Resolve` 的原逻辑只要 `modelID == ""` 就取 `defaults.model`，而 `defaults.model` 属于默认供应商。
用户只改了一项，却要为"供应商与模型两项都对不上"买单，而且错误信息把他指向 `active_model`。

改为：只有当供应商也落回默认那家时才采用 `defaults.model`，否则退到该供应商的第一个模型
（新增 `Registry.FirstModel`；查找仍归 registry 管，config 不自己遍历 `Providers`）。

修后同一场景：`缺少密钥：请设置环境变量 DEEPSEEK_API_KEY（供应商 "deepseek"）`，
配好 key 后日志为 `provider=deepseek model=deepseek-chat base_url=https://api.deepseek.com/v1`。

## 4. 验证

`go test ./...` 全绿；`bash scripts/check.sh` 全部通过（`-race` 因本机无 gcc 跳过）。

真实二进制（临时目录构建，`USERPROFILE` 指向临时 home 以隔离配置）：

| 场景 | 结果 |
|---|---|
| 无 `OPENAI_API_KEY` | `错误：缺少密钥：请设置环境变量 OPENAI_API_KEY（供应商 "openai"）…`，退出 1，不进界面 ✅ |
| 有 key | 解析通过 → 走到 TTY 检查 `错误：交互式界面需要终端…`（说明解析未挡路）✅ |
| `--version` 且无 key | `sycode dev`，退出 0 ✅ |
| `configs.json` 坏 JSON | `解析配置 "…" 失败：invalid character '}'…（第 32 字节附近）`，退出 1 ✅ |
| `active_provider=deepseek` 无 key | 提示 `DEEPSEEK_API_KEY` ✅ |
| `active_provider=deepseek` + key | `provider=deepseek model=deepseek-chat` ✅ |
| `active_provider=local`（无密钥供应商） | 不要求密钥，直接进 TTY 检查 ✅ |
| `SYCODE_LOG_LEVEL=debug` | 日志含 provider/model/base_url，**无密钥内容** ✅ |

界面标题显示模型名这一环由 `repl_test`（`render()` 含 `Model`）+ 新增的
`TestRootCommand_PassesResolvedModelToREPL` 合起来覆盖；工具 shell 无 TTY，未做人工目视。

## 5. 遗留

- 命令行 `--provider`/`--model` 还没接（`Resolve` 已留参数位），P4 做配置覆盖时加。
- `sycode run` 仍返回"尚未实现"（P4），因此 headless 路径不会触发解析——符合预期。
- `.ai-interactions/outputs/phase-2/output-2_impl-config.md` 的改动表把 `ResolveKey` 记成独立文件
  `key_resolver.go`，实际在 `config.go` 里（行数是设计值不是实测值）。
