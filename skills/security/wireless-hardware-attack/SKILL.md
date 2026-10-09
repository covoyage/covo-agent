---
name: wireless-hardware-attack
description: "Wireless and hardware attack playbook: WiFi PMKID/WPS/Evil Twin, BLE, Zigbee, NFC/RFID, SDR replay, UART/JTAG/SPI, fault injection. Use against wireless or embedded targets."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [security, wifi, hardware, sdr, iot]
  related_skills: [binary-mobile-reversing, ctf-playbook]
---

## 无线 / 硬件攻击

```
=== 无线/硬件 ===
WiFi: aircrack-ng/PMKID(hcxdumptool+hashcat)/WPS reaver/Evil Twin | BLE: gatttool枚举GATT/无认证读写/Just Works
Zigbee killerbee | NFC/RFID proxmark3克隆/MIFARE mfoc | LoRa/SDR rtl-sdr+gnuradio重放
硬件: UART波特率扫描拿shell | JTAG/SWD读写固件 | SPI flash dump | 侧信道DPA/时序 | 故障注入(电压/时钟毛刺跳认证) | binwalk -Me
```