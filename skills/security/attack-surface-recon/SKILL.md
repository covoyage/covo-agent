---
name: attack-surface-recon
description: "Recon and attack-surface mapping playbook: passive-first collection, subdomain/content pipelines, CDN and wildcard-DNS traps, fingerprint-first prioritization. Use when starting recon or mapping an unfamiliar target."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, pentest, recon, osint, subdomain]
  related_skills: [web-attack-methods, component-vuln-intel, pentest-verification]
---

## 侦察 / 攻击面测绘

本项目 recipe 对照:subfinder/amass/dnsenum(子域)、katana/gau/waybackurls(URL)、ffuf/gobuster/feroxbuster(内容)、nmap/rustscan/masscan(端口)、curl/wafw00f(指纹)、shodan_search/fofa_search/quake_search/zoomeye_search(资产引擎);未列入 recipe 的工具(httpx/dnsx/naabu 等)用 bash 直接跑。

```
=== 侦察/攻击面测绘(开局第一动作,60%战时在这,别裸奔进利用;端口/指纹等立即 finding_upsert/asset_upsert) ===
被动先行(不碰目标,全部执行):
  whois {domain} | grep -iE 'org|name|email|registrant'
  curl -s "https://crt.sh/?q=%.{domain}&output=json" | python3 -c "import sys,json;[print(x['name_value']) for x in json.load(sys.stdin)]" | sort -u
  dig +short {domain} @114.114.114.114; dig +short {domain} @8.8.8.8 (对比差异=CDN)
  curl -s "https://web.archive.org/cdx/search/cdx?url=*.{domain}/*&output=text&fl=original&collapse=urlkey" | head -100
  web_fetch: https://fofa.info/result?qbase64=$(echo -n 'domain="{domain}"' | base64)
主动流水线:
  scan recipe=subfinder → amass -passive → sort -u → 存活解析(httpx/dnsx) → httpx -title -tech-detect -cdn
  scan recipe=masscan/nmap → katana/gau 抓 URL → JS 里 grep (api|secret|key|token|password|aws|endpoint)
  scan recipe=ffuf(注意 catch-all 大小) → scan recipe=nuclei -severity medium,high,critical
  → httpx 识别到框架/版本 → 立即触发 component-vuln-intel 联网情报
🚨DNS地域限制绕过: 国内CDN常对海外DNS返0.0.0.0,用114.114.114.114解析才拿真实IP。多DNS对比(1.1.1.1 vs 114.114.114.114 vs 8.8.8.8),差异即CDN地域策略。
🚨CDN响应过滤绕过: CDN对特定URL模式(admin/actuator)返502而非转发: 502=被过滤存在,404=真不存在,403=路由拒绝。用502/404/403差异枚举存活端点;节点多端口常代理同一后端但规则不同。
🚨Nginx Catch-All陷阱: 所有路径返200同大小=catch-all(不是文件存在!)。三个随机路径对比size一致即catch-all;.php后缀可区分真404。
🚨DNS通配符CDN: 随机子域也解析到CDN=通配符DNS,子域枚举无意义,真实IP从历史DNS/证书透明/同服务器其他域名/邮件头/SSRF获取。
🚨宝塔面板识别: Cookie为32位hex + 302跳/login + "入口校验失败" = BT Panel随机入口。入口路径爆破 | 历史CVE | 默认FTP/MySQL复用。
🚨Nginx 404差分指纹: 真实404(146B) vs catch-all(大页面) 差分确认文件存在;批量探测CMS标志路径按响应大小分类。
🚨同IP多站横向: 反查IP找同服务器其他域名→弱站突破→宝塔等统一面板=横向所有站。
指纹优先级: 一旦识别出框架版本 → 立即转 component-vuln-intel 联网情报。先测绘清攻击面,再选最肥的边,不要看到一个洞就一头扎进去。
```