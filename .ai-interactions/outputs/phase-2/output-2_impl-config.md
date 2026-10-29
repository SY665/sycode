# 实施记录：P2 单元 2.2 配置加载与密钥解析

- 日期：2026-10-29
- 结论：完成，12 组测试通过；未接进启动流程（下一单元）

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/config/config.go` | 175 | `Config`/`ProviderConfig`/`Resolved`、`Load`、`DefaultPath`、`Resolve`、私有 `offsetOf` |
| `internal/config/key_resolver.go` | 30 | `ResolveKey` |
| `internal/config/config_test.go` | 215 | 12 组：缺文件、坏 JSON、三级优先级、端点覆盖、两类查找失败、密钥三态、默认路径 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| 配置文件不存在**不报错** | 首次运行没有配置是常态，应回退注册表默认值；报错会让新用户开箱即失败 |
| 三级优先级（参数 > 配置 > 注册表） | 命令行临时切换不该被配置文件覆盖；配置文件又该盖过内置默认 |
| 区分"未知供应商"与"模型不属于该供应商" | 两种错误指向不同的修改项（`active_provider` vs `active_model`），混在一起用户只能猜 |
| `ResolveKey` 对无密钥供应商返回 `("", nil)` | 本机服务没有密钥是正常状态，不该被当成错误处理 |
| 错误只提环境变量名 | 错误信息会被写进日志与终端，回显密钥等于泄露（§8 红线） |

## 3. 抓到的逻辑 bug

第一版判断"供应商是否存在"写成：

```go
if _, known, _ := reg.Find(providerID, ""); known || hasProvider(reg, providerID) {
```

但 `Find` 在"供应商命中、模型为空"时返回 `ok = false`（模型没找到），所以 `known` 恒为假，
分支永远不生效——所有查找失败都会被报成"未知供应商"，用户拿不到正确方向。
改用 `provider.ID != ""` 判断（`Find` 的契约本就承诺：供应商命中时返回 Provider）。
这正是 `Find` 返回 `Provider` 而不只是 `bool` 的价值。

## 4. 验证

```
go test ./internal/config/ -v
→ 12 组全 PASS
bash scripts/check.sh
→ 全部通过
```

覆盖到 `TestResolveModelNotBelongingToProvider` 这一条，专门断言"不能被误报成未知供应商"。

## 5. 遗留

- 尚未接线：`Load`/`Resolve` 还没被 `run()` 调用（下一单元）。
- 配置文件只读，没有写回能力（`/model` 切换要改配置时再做，届时需 `filePerm` 常量）。
- 未处理 `SYCODE_*` 环境变量层（原计划的"env 层"暂缓，命令行参数已够 P2 用）。
