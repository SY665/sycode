# 实施记录：P2 单元 2.1 模型注册表

- 日期：2026-10-29
- 结论：完成，12 组子测试通过；引入 `gopkg.in/yaml.v3`（人已批准）

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/providers/registry.yaml` | 66 | 3 个供应商 / 4 个模型：openai、deepseek、local（占位）；带字段说明与"数值需按官方文档核对"的提示 |
| `internal/providers/registry.go` | 155 | `Load`/`LoadFile`/`Find`/`Default` + 私有 `parse`/`validate` |
| `internal/providers/registry_test.go` | 165 | 内嵌表可用、查找命中与未命中、合法/非法注册表 6 例、YAML 语法错误 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| `BaseURL`/`APIKeyEnv` 放进注册表而非用户配置 | 供应商的固有能力，不该让每个用户重复填；用户配置只负责选择与覆盖 |
| `Find` 在"供应商命中、模型未命中"时仍返回 Provider | 调用方能区分"供应商名写错"和"模型名写错"，错误提示才能给对方向 |
| 校验失败一律返回错误 | 注册表是手改文件，字段名写错时最需要的是立刻报错，而不是静默用零值 |
| `LoadFile(io.Reader)` 与 `Load()` 共用 `parse` | 测试不必依赖内嵌文件，将来也支持自定义注册表 |
| 内嵌而非外置文件 | 开箱可用，不增加安装步骤；用户要自定义时走配置覆盖（P2.2） |

## 3. 踩到的点

**函数数超了单元上限。** 第一版顺手写了 `FindProvider`、`EnvKey`、`HasModel` 和一行 `var _ = os.Getenv` 占位，等于 9 个新函数 + 死代码，违反 CODING-STANDARDS §12（不留死代码）与 WORKFLOW §4.1（≤6 个新函数）。全部删掉，只留 `Load`/`LoadFile`/`Find`/`Default` + 两个私有函数。**没有被用到的导出就是负债**。

## 4. 验证

```
go test ./internal/providers/ -v
→ 12 个用例全 PASS（含 6 种非法注册表）
bash scripts/check.sh
→ 全部通过
```

非法用例覆盖：缺 defaults、供应商 id 重复、模型 id 重复、`context_window` 为 0、默认模型不存在、供应商无模型。错误信息都是中文且指出具体哪个 id 出问题。

## 5. 遗留

- 注册表里的 `context_window` 是起始值，**上线前要按各供应商官方文档核对**；已在 yaml 注释里标明。
- 尚未与 `config` 打通：`Load()` 还没被任何地方调用（下一单元接线）。
- 用户自定义注册表的入口未做（P2.2 的配置覆盖会覆盖它）。
