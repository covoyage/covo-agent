---
name: source-code-hunting
description: "Source code hunting: .git/.svn leaks, dangerous-function grep, JS deobfuscation, secrets deep-scan, patch diffing, supply-chain/CI vectors. Use when source or bundles are reachable."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, source-code, secrets, supply-chain, sast]
  related_skills: [web-attack-methods, binary-mobile-reversing, capability-primitive-search]
---

## 源码狩猎

```
=== 源码狩猎 ===
.git泄露: git-dumper → git log -p --all(已删敏感文件) | .svn/.DS_Store/composer.lock
危险函数grep: exec/system/eval/unserialize/pickle.loads/render/curl_exec + 硬编码密钥(sk-/ghp_/BEGIN RSA)
JS混淆破解(RC4+base64字符串数组模式): 提取字符串数组→找解码函数→找rotation IIFE→Node.js重建解码器批量解码→明文变量名/API路径/配置
静态扫描: semgrep --config=auto 快扫 / CodeQL建库写query
Secrets深挖: trufflehog/gitleaks 扫git全历史+docker镜像层+npm/PyPI tarball+前端bundle(--only-verified区分死活密钥)
框架Patch Diff: clone前后版本 diff → 修了什么=漏洞在哪 | 依赖链: composer.json/npm audit/pip-audit
供应链/CI: 依赖混淆(内部包名抢注公共registry) | GHA命令注入${{github.event.issue.title}} | self-hosted runner接管 | .npmrc/.pypirc凭据
```

## 白盒证据要求

- 每个源码候选点都必须记录仓库相对路径和行号，并说明输入到危险操作之间的控制流。
- 静态规则命中只是待验证假设；只有运行时 PoC 或已记录的 HTTP exchange 能把它提升为 confirmed。
- 交接给 verifier 时写清目标、源码位置、已有证据、单一验证目标和成功判据。
- 扫过但未确认的路径写入 coverage_upsert，注明 blocked、untested 或停止原因。