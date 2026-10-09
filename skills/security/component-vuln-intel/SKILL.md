---
name: component-vuln-intel
description: "Component-to-CVE intel sweep: the moment a framework/version is fingerprinted, run the full online search sequence (CVE DBs, PoC repos, CN communities, asset engines) before attacking. Use after fingerprinting any component."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, pentest, cve, intel, osint]
  related_skills: [attack-surface-recon, pentest-verification, web-attack-methods]
---

## 联网情报收集(识别组件→立即全网搜;结果=线索/待验证,执行验证前不是 verified finding)

```
🔴一识别出框架/组件/版本 → 停本地扫描立即联网(不搜就利用=盲打)。以下序列全部执行,用 {C}=组件名 {V}=版本 替换:

1.CVE漏洞库(必做):
  searchsploit {C} {V}
  web_fetch: https://github.com/advisories?query={C}+{V}
  web_fetch: https://www.cvedetails.com/google-search-results.php?q={C}+{V}&sa=Search

2.搜索引擎(至少3个):
  web_fetch: https://www.google.com/search?q={C}+{V}+exploit+PoC+RCE+site:github.com
  web_fetch: https://www.baidu.com/s?wd={C}+{V}+漏洞+利用+poc+getshell
  web_fetch: https://duckduckgo.com/?q={C}+{V}+vulnerability+exploit

3.中文安全社区(必做,中文首发多):
  web_fetch: https://xz.aliyun.com/search?keyword={C}+漏洞
  web_fetch: https://www.seebug.org/search/?keywords={C}
  web_fetch: https://www.freebuf.com/search?search={C}+{V}
  web_fetch: https://ti.qianxin.com/vulnerability?keyword={C}

4.GitHub搜PoC/exploit代码(必做):
  web_fetch: https://api.github.com/search/repositories?q={C}+{V}+exploit+OR+poc+OR+CVE&sort=updated&per_page=10
  web_fetch: https://api.github.com/search/repositories?q={C}+CVE&sort=stars&per_page=5
  找到仓库后读 README 拿用法

5.资产引擎(找同类目标/暴露面):
  web_fetch: https://fofa.info/result?qbase64=$(echo -n 'app="{C}"' | base64)
  web_fetch: https://www.shodan.io/search?query={C}+{V}
  scan recipe=zoomeye_search / quake_search / fofa_search / virustotal_search

命中后: PoC→verifier 验证→finding_upsert(verified+evidence); 线索→open 状态+"待验证"注明。
```