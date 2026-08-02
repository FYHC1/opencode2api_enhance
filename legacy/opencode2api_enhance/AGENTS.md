# AGENTS — opencode2api

本仓库 AI 代理与协作者的**入口说明**。

## 两套互补能力

| 能力 | 用途 | 入口 |
|------|------|------|
| **阶段化计划驱动** | 跨会话：定阶段 → 计划 → 交接执行 → 独立验收 | `docs/ai-framework/phased-plan-driven.md` |
| **提交前审查** | 单次 diff：风格 / 测试 / 依赖 | `CODE_REVIEW.md`、`/review` |

详细阶段工作流见：`docs/ai-framework/phased-plan-driven.md`。  
空白计划骨架：`docs/ai-framework/phase-plan.template.md`。  
阶段实例目录：`docs/ai-framework/plans/`（若已有 `docs/superpowers/plans/` 可继续用）。

## 强制遵循

1. **大块工作**先写阶段计划（含验收表与交接提示词），再实现；不要无计划的大范围编码。  
2. **验收认证据**：测试/构建命令实际跑过；禁止「应该能过」。  
3. **密钥不进 git**；破坏性操作与 push 远程需人类明确授权。  
4. **Git 提交**见下文规范；默认不 push。  
5. 编码与架构约定见 `coding-standards.md`、`architecture.md`（若存在）。

## OpenCode 命令

### 阶段协作

| 命令 | 子代理 | 用途 |
|------|--------|------|
| `/plan-phase` | `@phase-planner` | 起草阶段计划 + 文末交接提示词 |
| `/accept-phase` | `@phase-acceptor` | 对照计划独立验收（四段结论） |
| `/handoff` | — | 从已有计划生成可粘贴执行提示词 |

### 提交前审查

| 命令 | 子代理 | 用途 |
|------|--------|------|
| `/test` | `@test-engineer` | 测试编写与运行 |
| `/audit` | `@project-auditor` | 项目自检与升级建议 |
| `/deps` | `@dependencies-checker` | 依赖检查 |
| `/style` | `@code-stylespector` | 代码风格 |
| `/review` | 综合 | 依次风格 + 测试 +（如有）依赖 |

## Git 提交规范

```
<gitmoji><type>(<scope>): <中文描述>
```

| type | 说明 | gitmoji |
|------|------|---------|
| feat | 新功能 | ✨ |
| fix | 修复 bug | 🐛 |
| docs | 文档更新 | 📝 |
| style | 代码格式（不影响功能） | 🎨 |
| refactor | 重构 | ♻️ |
| test | 测试 | ✅ |
| chore | 构建/工具 | 🔧 |
| perf | 性能 | ⚡ |
| ci | CI/CD | 🐳 |
| revert | 回滚 | ⏪ |

- 描述使用**中文**，祈使语气，结尾不加句号  
- 首行尽量不超过 50 字符  

### 分支命名（建议）

```
feat/<topic> | fix/<topic> | chore/<topic>
```

阶段工作常用：`feat/phase-x-<slug>`。

### 提交前

在 `git commit` 前可询问是否 `/review` 或按 `CODE_REVIEW.md` 检查。  
**整阶段交付**另用 `/accept-phase`，与单次 review 不互相替代。

## 原则四条（阶段工作）

1. 边界先于功能  
2. 计划必须可交接（零上下文提示词）  
3. 任务必须可验证（命令 + 期望）  
4. 验收独立且认证据（缺陷显式带入下阶段）
