---
name: active-directory-attack
description: "Active Directory / internal network attack playbook: BloodHound, Kerberoast, ADCS ESC1/ESC8, NTLM relay, coerce, DACL abuse, DCSync, mitm6, LLMNR. Use when attacking AD, ADCS, NTLM relay, or internal domains."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, pentest, active-directory, kerberos, ntlm, bloodhound]
  related_skills: [post-exploitation, pentest-verification]
---

# Active Directory Attack — 内网域攻击

仅用于已授权目标。本项目 scan recipe 对照:bloodhound(域信息收集)、responder(LLMNR/NBT-NS)、
netexec(smb/ldap/winrm 枚举与验证)、impacket(secretsdump/GetNPUsers/GetUserSPNs 等脚本)、
hashcat(Kerberoast/AS-REP hash 破解)。

```
=== 内网域(2023+真实主战场) ===
侦察: scan recipe=bloodhound(SharpHound收集→攻击路径) | Kerberos: impacket GetNPUsers(AS-REP)/GetUserSPNs(Kerberoast)
🚨ADCS(Certipy一把梭): certipy find -vulnerable | ESC1指定SAN申域管证书 | ESC8 relay到CA拿DC证书
🚨NTLM Relay(比PtH重要,PtH常被EDR拦): ntlmrelayx -t ldap --escalate-user / -t http CA --adcs(ESC8) / RBCD
🚨强制认证Coerce: PetitPotam(MS-EFSRPC)/coercer全协议喷/printerbug → 喂给relay
🚨DACL滥用: WriteDACL→给自己加DCSync | 影子凭据certipy shadow(GenericWrite即可,不改密码不留痕)
DCSync: impacket secretsdump -just-dc → krbtgt hash→Golden Ticket
🚨一击致命域CVE(先测,命中直接域管): Zerologon(CVE-2020-1472) | NoPac(CVE-2021-42278/42287) |
  PrintNightmare(CVE-2021-34527) | EternalBlue(MS17-010,老SMBv1)
🚨IPv6/mitm6(默认双栈内网必打): mitm6劫持DHCPv6+DNS→WPAD→ntlmrelayx到LDAP/ADCS(比LLMNR更隐蔽)
LLMNR/NBT-NS投毒: scan recipe=responder 抓NetNTLMv2→hashcat破/relay
Linux内网: Redis未授权(CONFIG SET dir写SSH key) | NFS showmount | Docker 2375
```

## 记录要求

- 域内每个确认的攻击路径(可 Kerberoast 账户、可写 DACL、可 relay 的主机)立即
  finding_upsert,asset_upsert 记录域控/CA/关键服务器。
- 拿到域管后继续:DCSync → Golden Ticket → 横向全部域内主机,把影响面做实再写报告。
