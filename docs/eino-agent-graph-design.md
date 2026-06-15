# Eino 框架集成 — 设计方案

> 状态：**已废弃 (Superseded)**
>
> 本文档描述的是基于 `compose.Graph` 的图编排方案，**最终未采用**。
> 实际实现使用的是 `adk.NewChatModelAgent`（ADK ChatModelAgent）+ 共享工具集，
> 详见 [`docs/agentic-rag-design.md`](./agentic-rag-design.md)。
>
> 废弃原因：
> - `compose.Graph` 需要手写所有节点函数和分支逻辑，复杂度高
> - `adk.NewChatModelAgent` 提供开箱即用的 ReAct 循环，LLM 自主决定工具调用顺序
> - ADK 方案代码量仅为 Graph 方案的 1/3，且更符合 LightRAG 理念
>
> 本文档保留作为设计探索的历史记录。

---

## 历史内容（2026-06-14，仅供参考）

### 设计思路

用 Eino 的 `compose.Graph` 替换硬编码 for 循环，步骤统一为图节点，状态流转通过 `ReadingAgentState`。

### 废弃的 Graph 结构

```
START → Router（问题分类） → Search/Graph → Verify → Generate → END
                                ↑              │
                                └── Rewrite ←──┘ (循环最多 3 次)
```

### 未实现的原因

1. 节点函数（router/search/verify/rewrite/generate）总计 ~300 行，维护成本高
2. 图编译在运行时验证，调试困难
3. ADK ChatModelAgent 的 ReAct 循环天然支持多轮工具调用，无需手动建图
4. 实际只需 4 个 tool + instruction，就达到了同样的效果
