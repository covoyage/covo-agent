---
name: redteam-opsec
description: "OPSEC and evasion discipline: rate limiting, traffic blending, minimal footprint, anti-forensics, progressive exposure, blockade pivoting. Use before and during noisy operations."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, opsec, evasion, redteam]
  related_skills: [initial-access-phishing, post-exploitation, pentest-verification]
---

## OPSEC / 隐蔽作战纪律

```
核心: 打得进 ≠ 打得稳。被发现即行动归零。每个动作执行前先问"这步在防守方眼里长什么样"。
🚨IP黑名单绕过(被ban后第一时间试): X-Forwarded-For: 随机IP(CDN信任此头时直接绕过应用层黑名单)
  验证: 正常请求返回黑名单+你的IP → 加XFF后返回200 → 后续都带 | 每N个请求换XFF值
速率与时序: 扫描限速(nmap -T2 --max-rate / nuclei -rl / ffuf -p延迟)避开WAF封禁+IDS阈值 | 高危动作低频+随机jitter | 贴合目标作息
流量混淆: 贴正常业务(常见UA/Referer/合法路径) | 重武器前先探防御(进程列表/EDR/SIEM agent特征)→有则静默手法优先
最小足迹: 内存执行优先不落盘(DDexec/memfd) | webshell强口令+非常见路径+功能伪装 | 隧道走443/DNS贴常见出站 | 工具用完即删(/dev/shm)
反取证: unset HISTFILE | 日志选择性删自己条目(truncate全清反而告警) | touch -r保mtime | 别动监控/审计服务
渐进暴露: 被动侦察→确认无强监控→主动扫描→利用落地。能公开情报拿到的绝不主动碰目标。每升一级问"值不值得暴露"。
> 隐蔽不是洁癖,是红队的生存能力。一次莽撞的全端口全速扫描就可能让整个行动归零。
```