---
name: binary-mobile-reversing
description: "APK/EXE/binary reversing playbook: UniApp/DCloud JS deobfuscation, Flutter libapp.so extraction, native .so string deobfuscation, mobile general vectors, memory-corruption exploit chains. Use when reversing mobile apps or binaries."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, reversing, apk, android, pwn, firmware]
  related_skills: [ctf-playbook, post-exploitation, source-code-hunting]
---

## APK / EXE / 二进制逆向

本项目 recipe 对照:ghidra(headless 反编译)、checksec/radare2/objdump/ROPgadget/ropper/one-gadget/libc-database/pwninit(pwn 链)、binwalk/foremost(固件提取)、xxd/strings/gdb/volatility3(分析)。

```
=== APK/EXE/二进制 ===
APK: apktool d / jadx | Manifest看exported组件/deeplink/debuggable | rg硬编码密钥+API地址 | .so strings/ghidra | frida/objection动态
🚨UniApp/DCloud APK逆向(H5混合应用,业务全在JS不在DEX):
  识别: __UNI__XXXXXXX + assets/apps/ + uni-jsframework.js | manifest.json含"uni-app"字段
  核心文件: assets/apps/__UNI__XXX/www/app-service.js(800KB+混淆JS=全部业务逻辑) | jadx只能看壳,真逻辑在JS
  JS解混淆(RC4+字符串数组旋转): 提取a0G()大数组+a0m(idx,key)解码函数(base64→RC4)+rotation IIFE→拼可执行JS→node批量解码→JSON字典
  加密配置zlsioh.dat: header偏移表+block1(zlib→DEX)+block2(加密,含API域名列表←关键,需.so解密)+block3(zlib→类映射)
  .so字符串解混淆: 偶数位字符提取→反转 | XOR常量 | RC4+base64 | strings -n8找16字节等长串(AES密钥)/含点号串(域名)/x分隔IP串(HTTPDNS)
  高价值端点: /app_init /user/gustRegister(游客无验证注册) /user/getOssSts(OSS临时凭据!) /uploadFile2
🚨Flutter APK快速逆向(无需脱壳): 加固保护DEX层,但lib/arm64-v8a/libapp.so(Dart AOT)通常不加密
  strings -n8 libapp.so | grep 'https\?://' → 全部硬编码URL/API域名 | grep -E '^/' → API路径矩阵 | grep -iE 'key|secret|token|aws' → 凭据泄露
🚨Native .so字符串反混淆(通用): 混淆串在.rodata段,已知字符串对照混淆版→逆推算法→批量解码
🚨移动通用: 证书固定绕过(objection android sslpinning disable) | exported组件越权(drozer) | 深链接劫持 | 不安全存储(shared_prefs明文token) | WebView addJavascriptInterface/file://
  iOS: 砸壳(frida-ios-dump)+ghidra看Mach-O+objection动态
EXE/PE: file/strings | ghidra/IDA反编译找硬编码/加密/网络逻辑 | x64dbg动调 | 漏洞:溢出/格式化串/UAF/DLL劫持
🚨内存破坏exploit链(崩溃→RCE): checksec看防护 → 确定原语 → 信息泄露绕ASLR → ROP链(ROPgadget/pwntools ret2libc) → 堆利用(tcache poison) → 控制流劫持 | 固件IoT:binwalk -Me提取+qemu-user调试
```