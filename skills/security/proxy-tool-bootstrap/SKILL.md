---
name: proxy-tool-bootstrap
description: "Proxy pivoting and tool bootstrapping: region-matched SOCKS5/HTTP/Tor chains, per-tool proxy flags, Python reimplementation of missing tools, dictionary self-generation, OOB infrastructure. Use when blocked or missing tooling."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, proxy, pivot, bootstrap, oob]
  related_skills: [redteam-opsec, component-vuln-intel, pentest-verification]
---

## 自找代理 + 工具自举(被拦换路,没工具自己写)

```
🔴代理(收到拒绝/限流/超时 → 第一反应不是重试,是换路):
  执行序列(按顺序,前一步失败才下一步):
  ①探目标地区: curl -s "http://ip-api.com/json/{目标IP}" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d['country'],d['countryCode'])"
  ②获取该地区SOCKS5代理: curl -s "https://api.proxyscrape.com/v2/?request=displayproxies&protocol=socks5&country={countryCode}&timeout=5000" | head -20
  ③验证代理可达目标: curl --socks5 {PROXY_IP:PORT} --connect-timeout 8 -s -o /dev/null -w "%{http_code}" {目标URL}
  ④200=可用,立即用此代理重新执行被拦请求; 非200=换下一个重复③
  ⑤全部SOCKS5失败→HTTP代理 → ⑦全失败→Tor: curl --socks5 127.0.0.1:9050 --connect-timeout 15 {目标URL}
  工具统一加代理参数: curl --socks5 / sqlmap --proxy=socks5://{P} / nmap --proxies socks5://{P} / nuclei -proxy / ffuf -x
  轮换策略: 429/403→立即换,每20请求主动换 | Cloudflare→代理池+请求间隔2-5s随机
  🚨探测必须用SOCKS5: HTTP代理会插入自己的错误页导致无法区分目标实际响应
工具自举(which X || 用Python实现):
  无nmap→socket扫端口 | 无ffuf→requests爆目录 | 无sqlmap→手工payload检测 | 无hydra→requests爆破 | 无nuclei→requests打已知payload
  复杂工具用Python模拟: 爬虫requests+bs4 / 编码base64/hex / 哈希hashlib / 加密pycryptodome / 嗅探scapy
字典自生成: 基于目标域名/公司名造变体 | 从网页提关键词 | 用户名+年份+特殊字符组合 | 服务默认凭据
OOB基础设施(盲漏洞都靠它): interactsh-client拿oast.fun域名 | 或VPS python3 -m http.server/nc看回连 | ngrok/cloudflared隧道
  → 看到OOB回连(DNS查询/HTTP请求)才算确认 → finding_upsert(verified)
```