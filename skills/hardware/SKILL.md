---
name: hardware
description: "Interact with I2C, SPI, and Serial peripherals on embedded Linux boards."
version: 1.0.0
author: Covo Agent
platforms: [linux]
metadata:
  tags: [hardware, i2c, spi, serial, embedded, sensor, peripheral]
---

# Hardware (I2C / SPI / Serial)

Use `i2c`, `spi`, and `serial` tools to interact with sensors, displays, and other peripherals.

## Quick Start

```
# 1. Find available I2C buses and scan devices
i2c scan (bus: 1)

# 2. Read from a sensor register
i2c read (bus: 1, address: 0x38, register: 0xAC)

# 3. Write to a device register
i2c write (bus: 1, address: 0x3C, register: 0x00, data: [0xAE])

# 4. List serial ports
serial list

# 5. Write to serial port
serial write (port: "/dev/ttyUSB0", baud: 115200, data: "AT\r\n")
```

## Setup

```bash
# Enable I2C kernel module
sudo modprobe i2c-dev

# Check if SPI is available
ls /dev/spidev*

# Add user to hardware groups
sudo usermod -aG i2c,spi,dialout $USER
```

## Safety

- Write operations require user confirmation
- I2C addresses are 7-bit (0x03-0x77)
- Maximum 256 bytes per I2C transaction
- Always verify wiring before sending commands

## Common Devices

| Sensor | I2C Addr | Usage |
|--------|----------|-------|
| AHT20 | 0x38 | Temperature/humidity |
| BME280 | 0x76/0x77 | Pressure/temperature/humidity |
| SSD1306 | 0x3C | OLED display |
| MPU6050 | 0x68 | Accelerometer/gyroscope |
| DS3231 | 0x68 | RTC clock |
| INA219 | 0x40 | Power monitor |
| PCA9685 | 0x40 | PWM/servo driver |

## Troubleshooting

| Problem | Solution |
|---------|----------|
| No I2C buses | `modprobe i2c-dev` |
| Permission denied | Add user to `i2c` group |
| No devices on scan | Check wiring, pull-up resistors (4.7kΩ) |
| SPI returns zeros | Check MISO wiring and device power |
| Serial no response | Verify baud rate and TX/RX cross-wiring |
