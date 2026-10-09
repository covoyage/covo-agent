---
name: ctf-playbook
description: "CTF workflow playbook: challenge triage, category-to-recipe mapping (pwn, web, reverse, crypto, forensics, stego), flag capture discipline. Use when solving CTF challenges, analyzing binaries, or hunting flags."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, ctf, pwn, reverse, forensics, stego]
  related_skills: [post-exploitation, pentest-verification, web-attack-methods]
---

# CTF Playbook — 工作流与配方映射

## 挑战分诊(替代通用侦察)

CTF 的挑战描述就是目标说明:先从题面提取服务端点、附件或二进制,再决定动作;
只有远程题才需要扫描,纯文件题直接进分析,不做资产测绘。

## 类别 → recipe 映射

| 类别 | 首选 recipe / 工具 |
|---|---|
| pwn / 逆向 | `pwntools`(漏洞利用脚本,PWN_* 环境变量)、`angr`(符号执行)、`ghidra`(headless 反编译)、`checksec`、`radare2`、`objdump`、`ROPgadget`/`ropper`、`one-gadget`、`libc-database`、`pwninit` |
| web | `nmap`、`curl`、`ffuf`、`katana`、`dirsearch`;注入/认证手法见 `web-attack-methods` |
| crypto | `hashcat`、`john`、`hashpump`(哈希长度扩展)、`xxd`、`strings`;Padding Oracle/RSA 见 `post-exploitation` |
| forensics / stego | `binwalk`、`foremost`、`zsteg`、`steghide`、`exiftool`、`strings`、`volatility3`、`xxd` |
| 容器 / 杂项 | `trivy`、`kube-hunter`、`gdb`、`volatility3` |

用 `scan recipe "list"` 确认本机可用的完整目录。

## Flag 纪律

- 题面给出 flag 格式(flag{...}、CTF{...} 等)后,**每一步工具输出都要搜索该格式**,
  命中即停,不要继续扩大扫描。
- 附件与题目文件在工作区,先读文件再碰任何远程目标。
- 每个解出/证伪的思路按 `pentest-verification` 的标准落 finding,避免死磕重复路径。
