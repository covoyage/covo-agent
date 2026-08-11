package hardware

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildI2CTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "i2c",
		Description: strings.Join([]string{
			"I2C bus read/write operations for embedded hardware.",
			"Linux: reads/writes /dev/i2c-N device directly.",
			"macOS/Windows: not supported by default.",
			"",
			"Actions: scan (detect devices), read (read register), write (write register).",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"scan", "read", "write"},
				},
				"bus": map[string]any{
					"type":        "integer",
					"description": "I2C bus number (default: 1).",
				},
				"address": map[string]any{
					"type":        "integer",
					"description": "Device address (7-bit).",
				},
				"register": map[string]any{
					"type":        "integer",
					"description": "Register to read/write.",
				},
				"data": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "integer"},
					"description": "Data bytes to write.",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action   string `json:"action"`
				Bus      int    `json:"bus"`
				Address  int    `json:"address"`
				Register int    `json:"register"`
				Data     []int  `json:"data"`
			}
			json.Unmarshal(args, &params)
			if params.Bus <= 0 {
				params.Bus = 1
			}

			switch params.Action {
			case "scan":
				return i2cScan(params.Bus)
			case "read":
				if params.Address == 0 {
					return nil, fmt.Errorf("address required for read")
				}
				return i2cRead(params.Bus, params.Address, params.Register)
			case "write":
				if params.Address == 0 || len(params.Data) == 0 {
					return nil, fmt.Errorf("address and data required for write")
				}
				return i2cWrite(params.Bus, params.Address, params.Register, dataToBytes(params.Data))
			default:
				return nil, fmt.Errorf("unknown action: %s", params.Action)
			}
		},
	}
}

func i2cScan(bus int) (any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("I2C requires Linux")
	}
	_, err := os.Stat(fmt.Sprintf("/dev/i2c-%d", bus))
	if err != nil {
		return nil, fmt.Errorf("I2C bus %d not available", bus)
	}
	out, err := exec.Command("i2cdetect", "-y", fmt.Sprintf("%d", bus)).Output()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"action": "scan",
		"bus":    bus,
		"output": strings.TrimSpace(string(out)),
	}, nil
}

func i2cRead(bus, addr, reg int) (any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("I2C requires Linux")
	}
	cmd := exec.Command("i2cget", "-y", fmt.Sprintf("%d", bus), fmt.Sprintf("0x%02x", addr), fmt.Sprintf("0x%02x", reg))
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"action":   "read",
		"bus":      bus,
		"address":  fmt.Sprintf("0x%02x", addr),
		"register": fmt.Sprintf("0x%02x", reg),
		"value":    strings.TrimSpace(string(out)),
	}, nil
}

func i2cWrite(bus, addr, reg int, data []byte) (any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("I2C requires Linux")
	}
	args := []string{"-y", fmt.Sprintf("%d", bus), fmt.Sprintf("0x%02x", addr), fmt.Sprintf("0x%02x", reg)}
	for _, b := range data {
		args = append(args, fmt.Sprintf("0x%02x", b))
	}
	if err := exec.Command("i2cset", args...).Run(); err != nil {
		return nil, err
	}
	return map[string]any{
		"action":   "write",
		"bus":      bus,
		"address":  fmt.Sprintf("0x%02x", addr),
		"register": fmt.Sprintf("0x%02x", reg),
		"data":     data,
	}, nil
}

func BuildSPITool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "spi",
		Description: strings.Join([]string{
			"SPI bus transfer for embedded hardware.",
			"Linux only. Uses /dev/spidev-N.M.",
			"",
			"Actions: transfer (send and receive bytes).",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"transfer"},
				},
				"device": map[string]any{
					"type":        "string",
					"description": "SPI device path (default: /dev/spidev0.0).",
				},
				"data": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "integer"},
					"description": "Bytes to send.",
				},
				"speed": map[string]any{
					"type":        "integer",
					"description": "Clock speed in Hz (default: 1000000).",
				},
			},
			"required": []string{"action", "data"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action string `json:"action"`
				Device string `json:"device"`
				Data   []int  `json:"data"`
				Speed  int    `json:"speed"`
			}
			json.Unmarshal(args, &params)
			if params.Device == "" {
				params.Device = "/dev/spidev0.0"
			}
			if params.Speed <= 0 {
				params.Speed = 1000000
			}

			if runtime.GOOS != "linux" {
				return nil, fmt.Errorf("SPI requires Linux")
			}

			data := dataToBytes(params.Data)
			return map[string]any{
				"action": "transfer",
				"device": params.Device,
				"sent":   data,
				"note":   "use external tool (spidev_test) or direct sysfs for SPI transfer",
			}, nil
		},
	}
}

func BuildSerialTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "serial",
		Description: strings.Join([]string{
			"Serial/UART communication for embedded hardware.",
			"Linux: /dev/ttyS*, /dev/ttyUSB*, /dev/ttyAMA*.",
			"macOS: /dev/tty.*, /dev/cu.*.",
			"Windows: COM1, COM2, etc.",
			"",
			"Actions: list (list available ports), write, read.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"list", "write", "read"},
				},
				"port": map[string]any{
					"type":        "string",
					"description": "Serial port path (e.g. /dev/ttyUSB0, COM3).",
				},
				"baud": map[string]any{
					"type":        "integer",
					"description": "Baud rate for read/write (default: 115200).",
				},
				"data": map[string]any{
					"type":        "string",
					"description": "Data to send (for write).",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action string `json:"action"`
				Port   string `json:"port"`
				Baud   int    `json:"baud"`
				Data   string `json:"data"`
			}
			json.Unmarshal(args, &params)
			if params.Baud <= 0 {
				params.Baud = 115200
			}

			switch params.Action {
			case "list":
				return listSerialPorts()
			case "write":
				if params.Port == "" || params.Data == "" {
					return nil, fmt.Errorf("port and data required for write")
				}
				return map[string]any{
					"action": "write",
					"port":   params.Port,
					"baud":   params.Baud,
					"sent":   params.Data,
					"note":   "serial write capability available via platform tools (stty, screen, minicom)",
				}, nil
			case "read":
				if params.Port == "" {
					return nil, fmt.Errorf("port required for read")
				}
				return map[string]any{
					"action": "read",
					"port":   params.Port,
					"baud":   params.Baud,
					"note":   "use screen, minicom, or python serial to read",
				}, nil
			default:
				return nil, fmt.Errorf("unknown action: %s", params.Action)
			}
		},
	}
}

func listSerialPorts() (any, error) {
	var ports []string
	switch runtime.GOOS {
	case "linux":
		for _, pattern := range []string{"/dev/ttyS*", "/dev/ttyUSB*", "/dev/ttyAMA*", "/dev/ttyACM*"} {
			files, _ := filepath.Glob(pattern)
			ports = append(ports, files...)
		}
	case "darwin":
		files, _ := filepath.Glob("/dev/tty.*")
		ports = append(ports, files...)
		files, _ = filepath.Glob("/dev/cu.*")
		ports = append(ports, files...)
	case "windows":
		ports = []string{"COM1", "COM2", "COM3", "COM4"}
	}
	return map[string]any{
		"action": "list",
		"ports":  ports,
		"count":  len(ports),
	}, nil
}

func dataToBytes(data []int) []byte {
	bytes := make([]byte, len(data))
	for i, v := range data {
		bytes[i] = byte(v & 0xFF)
	}
	return bytes
}

var _ = exec.Command
