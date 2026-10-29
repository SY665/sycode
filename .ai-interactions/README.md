# 留痕目录

这里保存**人与 AI 协作的真实过程**，是代码"为什么长这样"的第一手依据。规则见 [`../docs/WORKFLOW.md`](../docs/WORKFLOW.md) 第 2 节。

```
prompts/<phase>/prompt-N_<slug>.md   人说的话（原文）
outputs/<phase>/output-N_<slug>.md   agent 的产出（原文，不改写）
tasks/<issue-slug>/                  绑定具体 issue 的往返
STATE.md                             断点文件：现在在哪、下一步第一件事（每次收口更新）
```

`STATE.md` 的规则见 [`../docs/WORKFLOW.md`](../docs/WORKFLOW.md) §4.1：它只记"当前进度 + 下一步"，不写流水账（流水账归 git）。

三条底线：

1. **原文保存**，不润色、不补写、不改结论。
2. **不存敏感信息**：密钥、token、私有路径、他人隐私。
3. **编号成对**：prompt-N 对应 output-N；只发未产出就只留 prompt。

模板在 [`../docs/templates/`](../docs/templates/)。
