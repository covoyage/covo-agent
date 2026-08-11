package theme

import "sort"

// All returns all registered theme palettes.
func All() []*Palette {
	out := make([]*Palette, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dark != out[j].Dark {
			return out[i].Dark // dark first
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Get returns the palette by name (case-insensitive), or nil.
func Get(name string) *Palette {
	for _, p := range registry {
		if equalFold(p.Name, name) {
			return p
		}
	}
	return nil
}

// Names returns all registered theme names.
func Names() []string {
	all := All()
	out := make([]string, len(all))
	for i, p := range all {
		out[i] = p.Name
	}
	return out
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca := a[i]
		cb := b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

var registry = map[string]*Palette{
	// ========== DARK THEMES ==========

	"dracula": {
		Name: "dracula", Dark: true,
		Accent:  "#bd93f9",
		Success: "#50fa7b",
		Error:   "#ff5555",
		Warning: "#f1fa8c",
		Text:    "#f8f8f2",
		Muted:   "#6272a4",
		Dim:     "#44475a",
		System:  "#8be9fd",

		UserMessage:  "#bd93f9",
		AssistantText: "#f8f8f2",

		SelectedBg:    "#44475a",
		UserMessageBg: "#282a36",
		ToolSuccessBg: "#1e3a2a",
		ToolErrorBg:   "#3a1e1e",

		MdHeading: "#ff79c6",
		MdLink:    "#8be9fd",
		MdCode:    "#50fa7b",

		SyntaxComment:  "#6272a4",
		SyntaxKeyword:  "#ff79c6",
		SyntaxFunction: "#50fa7b",
		SyntaxString:   "#f1fa8c",
		SyntaxNumber:   "#bd93f9",
		SyntaxType:     "#8be9fd",
	},

	"nord": {
		Name: "nord", Dark: true,
		Accent:  "#88c0d0",
		Success: "#a3be8c",
		Error:   "#bf616a",
		Warning: "#ebcb8b",
		Text:    "#eceff4",
		Muted:   "#81a1c1",
		Dim:     "#4c566a",
		System:  "#81a1c1",

		UserMessage:  "#88c0d0",
		AssistantText: "#eceff4",

		SelectedBg:    "#434c5e",
		UserMessageBg: "#2e3440",
		ToolSuccessBg: "#2a3a2a",
		ToolErrorBg:   "#3a2a2a",

		MdHeading: "#81a1c1",
		MdLink:    "#5e81ac",
		MdCode:    "#a3be8c",

		SyntaxComment:  "#4c566a",
		SyntaxKeyword:  "#81a1c1",
		SyntaxFunction: "#88c0d0",
		SyntaxString:   "#a3be8c",
		SyntaxNumber:   "#b48ead",
		SyntaxType:     "#8fbcbb",
	},

	"catppuccin-mocha": {
		Name: "catppuccin-mocha", Dark: true,
		Accent:  "#cba6f7",
		Success: "#a6e3a1",
		Error:   "#f38ba8",
		Warning: "#f9e2af",
		Text:    "#cdd6f4",
		Muted:   "#a6adc8",
		Dim:     "#585b70",
		System:  "#89b4fa",

		UserMessage:  "#cba6f7",
		AssistantText: "#cdd6f4",

		SelectedBg:    "#45475a",
		UserMessageBg: "#1e1e2e",
		ToolSuccessBg: "#1e3a2a",
		ToolErrorBg:   "#3a1e2e",

		MdHeading: "#f5c2e7",
		MdLink:    "#89b4fa",
		MdCode:    "#a6e3a1",

		SyntaxComment:  "#6c7086",
		SyntaxKeyword:  "#cba6f7",
		SyntaxFunction: "#fab387",
		SyntaxString:   "#a6e3a1",
		SyntaxNumber:   "#fab387",
		SyntaxType:     "#89b4fa",
	},

	"catppuccin-latte": {
		Name: "catppuccin-latte", Dark: false,
		Accent:  "#8839ef",
		Success: "#40a02b",
		Error:   "#d20f39",
		Warning: "#df8e1d",
		Text:    "#4c4f69",
		Muted:   "#9ca0b0",
		Dim:     "#bcc0cc",
		System:  "#1e66f5",

		UserMessage:  "#8839ef",
		AssistantText: "#4c4f69",

		SelectedBg:    "#ccd0da",
		UserMessageBg: "#eff1f5",
		ToolSuccessBg: "#dce8dc",
		ToolErrorBg:   "#e8dcdc",

		MdHeading: "#ea76cb",
		MdLink:    "#1e66f5",
		MdCode:    "#40a02b",

		SyntaxComment:  "#9ca0b0",
		SyntaxKeyword:  "#8839ef",
		SyntaxFunction: "#fe640b",
		SyntaxString:   "#40a02b",
		SyntaxNumber:   "#fe640b",
		SyntaxType:     "#1e66f5",
	},

	"one-dark": {
		Name: "one-dark", Dark: true,
		Accent:  "#61afef",
		Success: "#98c379",
		Error:   "#e06c75",
		Warning: "#e5c07b",
		Text:    "#abb2bf",
		Muted:   "#5c6370",
		Dim:     "#4b5263",
		System:  "#56b6c2",

		UserMessage:  "#61afef",
		AssistantText: "#abb2bf",

		SelectedBg:    "#3e4451",
		UserMessageBg: "#282c34",
		ToolSuccessBg: "#1e3a2a",
		ToolErrorBg:   "#3a1e1e",

		MdHeading: "#e06c75",
		MdLink:    "#61afef",
		MdCode:    "#98c379",

		SyntaxComment:  "#5c6370",
		SyntaxKeyword:  "#c678dd",
		SyntaxFunction: "#e5c07b",
		SyntaxString:   "#98c379",
		SyntaxNumber:   "#d19a66",
		SyntaxType:     "#56b6c2",
	},

	"one-light": {
		Name: "one-light", Dark: false,
		Accent:  "#4078f2",
		Success: "#50a14f",
		Error:   "#e45649",
		Warning: "#c18401",
		Text:    "#383a42",
		Muted:   "#a0a1a7",
		Dim:     "#c0c1c7",
		System:  "#0184bc",

		UserMessage:  "#4078f2",
		AssistantText: "#383a42",

		SelectedBg:    "#e5e5e5",
		UserMessageBg: "#fafafa",
		ToolSuccessBg: "#e8f0e8",
		ToolErrorBg:   "#f0e8e8",

		MdHeading: "#e45649",
		MdLink:    "#4078f2",
		MdCode:    "#50a14f",

		SyntaxComment:  "#a0a1a7",
		SyntaxKeyword:  "#a626a4",
		SyntaxFunction: "#c18401",
		SyntaxString:   "#50a14f",
		SyntaxNumber:   "#986801",
		SyntaxType:     "#0184bc",
	},

	"gruvbox-dark": {
		Name: "gruvbox-dark", Dark: true,
		Accent:  "#fe8019",
		Success: "#b8bb26",
		Error:   "#fb4934",
		Warning: "#fabd2f",
		Text:    "#ebdbb2",
		Muted:   "#928374",
		Dim:     "#665c54",
		System:  "#83a598",

		UserMessage:  "#fe8019",
		AssistantText: "#ebdbb2",

		SelectedBg:    "#504945",
		UserMessageBg: "#282828",
		ToolSuccessBg: "#2a3a2a",
		ToolErrorBg:   "#3a2a2a",

		MdHeading: "#fe8019",
		MdLink:    "#83a598",
		MdCode:    "#b8bb26",

		SyntaxComment:  "#928374",
		SyntaxKeyword:  "#fb4934",
		SyntaxFunction: "#fe8019",
		SyntaxString:   "#b8bb26",
		SyntaxNumber:   "#d3869b",
		SyntaxType:     "#8ec07c",
	},

	"gruvbox-light": {
		Name: "gruvbox-light", Dark: false,
		Accent:  "#cc241d",
		Success: "#79740e",
		Error:   "#9d0006",
		Warning: "#b57614",
		Text:    "#3c3836",
		Muted:   "#928374",
		Dim:     "#bdae93",
		System:  "#076678",

		UserMessage:  "#cc241d",
		AssistantText: "#3c3836",

		SelectedBg:    "#d5c4a1",
		UserMessageBg: "#fbf1c7",
		ToolSuccessBg: "#e8f0e8",
		ToolErrorBg:   "#f0e8e8",

		MdHeading: "#cc241d",
		MdLink:    "#076678",
		MdCode:    "#79740e",

		SyntaxComment:  "#928374",
		SyntaxKeyword:  "#9d0006",
		SyntaxFunction: "#cc241d",
		SyntaxString:   "#79740e",
		SyntaxNumber:   "#8f3f71",
		SyntaxType:     "#427b58",
	},

	"solarized-dark": {
		Name: "solarized-dark", Dark: true,
		Accent:  "#268bd2",
		Success: "#859900",
		Error:   "#dc322f",
		Warning: "#b58900",
		Text:    "#839496",
		Muted:   "#657b83",
		Dim:     "#586e75",
		System:  "#2aa198",

		UserMessage:  "#268bd2",
		AssistantText: "#839496",

		SelectedBg:    "#073642",
		UserMessageBg: "#002b36",
		ToolSuccessBg: "#0a2a0a",
		ToolErrorBg:   "#2a0a0a",

		MdHeading: "#b58900",
		MdLink:    "#268bd2",
		MdCode:    "#859900",

		SyntaxComment:  "#586e75",
		SyntaxKeyword:  "#859900",
		SyntaxFunction: "#268bd2",
		SyntaxString:   "#2aa198",
		SyntaxNumber:   "#d33682",
		SyntaxType:     "#b58900",
	},

	"solarized-light": {
		Name: "solarized-light", Dark: false,
		Accent:  "#268bd2",
		Success: "#859900",
		Error:   "#dc322f",
		Warning: "#b58900",
		Text:    "#657b83",
		Muted:   "#93a1a1",
		Dim:     "#bcc8c8",
		System:  "#2aa198",

		UserMessage:  "#268bd2",
		AssistantText: "#657b83",

		SelectedBg:    "#eee8d5",
		UserMessageBg: "#fdf6e3",
		ToolSuccessBg: "#e8f0e8",
		ToolErrorBg:   "#f0e8e8",

		MdHeading: "#b58900",
		MdLink:    "#268bd2",
		MdCode:    "#859900",

		SyntaxComment:  "#93a1a1",
		SyntaxKeyword:  "#859900",
		SyntaxFunction: "#268bd2",
		SyntaxString:   "#2aa198",
		SyntaxNumber:   "#d33682",
		SyntaxType:     "#b58900",
	},

	"tokyo-night": {
		Name: "tokyo-night", Dark: true,
		Accent:  "#7aa2f7",
		Success: "#73daca",
		Error:   "#f7768e",
		Warning: "#e0af68",
		Text:    "#c0caf5",
		Muted:   "#565f89",
		Dim:     "#3b4261",
		System:  "#7dcfff",

		UserMessage:  "#7aa2f7",
		AssistantText: "#c0caf5",

		SelectedBg:    "#3b4261",
		UserMessageBg: "#1a1b26",
		ToolSuccessBg: "#1e2a2a",
		ToolErrorBg:   "#2a1e2a",

		MdHeading: "#7dcfff",
		MdLink:    "#7aa2f7",
		MdCode:    "#73daca",

		SyntaxComment:  "#565f89",
		SyntaxKeyword:  "#bb9af7",
		SyntaxFunction: "#7aa2f7",
		SyntaxString:   "#9ece6a",
		SyntaxNumber:   "#ff9e64",
		SyntaxType:     "#2ac3de",
	},

	"everforest-dark": {
		Name: "everforest-dark", Dark: true,
		Accent:  "#83c092",
		Success: "#a7c080",
		Error:   "#e69875",
		Warning: "#dbbc7f",
		Text:    "#d3c6aa",
		Muted:   "#7a8478",
		Dim:     "#5a6a5a",
		System:  "#7fbbb3",

		UserMessage:  "#83c092",
		AssistantText: "#d3c6aa",

		SelectedBg:    "#4a5a4a",
		UserMessageBg: "#2d353b",
		ToolSuccessBg: "#1e2a1e",
		ToolErrorBg:   "#2a1e1e",

		MdHeading: "#83c092",
		MdLink:    "#7fbbb3",
		MdCode:    "#a7c080",

		SyntaxComment:  "#7a8478",
		SyntaxKeyword:  "#e69875",
		SyntaxFunction: "#83c092",
		SyntaxString:   "#a7c080",
		SyntaxNumber:   "#dbbc7f",
		SyntaxType:     "#7fbbb3",
	},

	"everforest-light": {
		Name: "everforest-light", Dark: false,
		Accent:  "#83c092",
		Success: "#a7c080",
		Error:   "#e69875",
		Warning: "#dbbc7f",
		Text:    "#5c6a5c",
		Muted:   "#9da99d",
		Dim:     "#c0ccc0",
		System:  "#7fbbb3",

		UserMessage:  "#83c092",
		AssistantText: "#5c6a5c",

		SelectedBg:    "#d0dcd0",
		UserMessageBg: "#fdf6e3",
		ToolSuccessBg: "#e8f0e8",
		ToolErrorBg:   "#f0e8e8",

		MdHeading: "#83c092",
		MdLink:    "#7fbbb3",
		MdCode:    "#a7c080",

		SyntaxComment:  "#9da99d",
		SyntaxKeyword:  "#e69875",
		SyntaxFunction: "#83c092",
		SyntaxString:   "#a7c080",
		SyntaxNumber:   "#dbbc7f",
		SyntaxType:     "#7fbbb3",
	},

	"ayu-dark": {
		Name: "ayu-dark", Dark: true,
		Accent:  "#ff8a3c",
		Success: "#aad94c",
		Error:   "#f07178",
		Warning: "#ffb454",
		Text:    "#b3b1ad",
		Muted:   "#707a8c",
		Dim:     "#4b5259",
		System:  "#39bae6",

		UserMessage:  "#ff8a3c",
		AssistantText: "#b3b1ad",

		SelectedBg:    "#4b5259",
		UserMessageBg: "#0f1419",
		ToolSuccessBg: "#1e2a1e",
		ToolErrorBg:   "#2a1e1e",

		MdHeading: "#ff8a3c",
		MdLink:    "#39bae6",
		MdCode:    "#aad94c",

		SyntaxComment:  "#707a8c",
		SyntaxKeyword:  "#ff7733",
		SyntaxFunction: "#ff8a3c",
		SyntaxString:   "#aad94c",
		SyntaxNumber:   "#ffb454",
		SyntaxType:     "#39bae6",
	},

	"ayu-light": {
		Name: "ayu-light", Dark: false,
		Accent:  "#ff8a3c",
		Success: "#86b300",
		Error:   "#f07178",
		Warning: "#f2ae49",
		Text:    "#5c6166",
		Muted:   "#8a9199",
		Dim:     "#b8c0c8",
		System:  "#399ee6",

		UserMessage:  "#ff8a3c",
		AssistantText: "#5c6166",

		SelectedBg:    "#d0d8e0",
		UserMessageBg: "#fafafa",
		ToolSuccessBg: "#e8f0e8",
		ToolErrorBg:   "#f0e8e8",

		MdHeading: "#ff8a3c",
		MdLink:    "#399ee6",
		MdCode:    "#86b300",

		SyntaxComment:  "#8a9199",
		SyntaxKeyword:  "#f07171",
		SyntaxFunction: "#ff8a3c",
		SyntaxString:   "#86b300",
		SyntaxNumber:   "#f2ae49",
		SyntaxType:     "#399ee6",
	},

	"rose-pine": {
		Name: "rose-pine", Dark: true,
		Accent:  "#c4a7e7",
		Success: "#9ccfd8",
		Error:   "#eb6f92",
		Warning: "#f6c177",
		Text:    "#e0def4",
		Muted:   "#908caa",
		Dim:     "#6e6a86",
		System:  "#31748f",

		UserMessage:  "#c4a7e7",
		AssistantText: "#e0def4",

		SelectedBg:    "#6e6a86",
		UserMessageBg: "#191724",
		ToolSuccessBg: "#1e2a3a",
		ToolErrorBg:   "#3a1e2a",

		MdHeading: "#eb6f92",
		MdLink:    "#31748f",
		MdCode:    "#9ccfd8",

		SyntaxComment:  "#6e6a86",
		SyntaxKeyword:  "#c4a7e7",
		SyntaxFunction: "#f6c177",
		SyntaxString:   "#9ccfd8",
		SyntaxNumber:   "#f6c177",
		SyntaxType:     "#31748f",
	},

	"rose-pine-dawn": {
		Name: "rose-pine-dawn", Dark: false,
		Accent:  "#907aa9",
		Success: "#56949f",
		Error:   "#b4637a",
		Warning: "#ea9d34",
		Text:    "#575279",
		Muted:   "#9893a5",
		Dim:     "#c0bcc8",
		System:  "#286983",

		UserMessage:  "#907aa9",
		AssistantText: "#575279",

		SelectedBg:    "#d0ccd8",
		UserMessageBg: "#faf4ed",
		ToolSuccessBg: "#e8ece8",
		ToolErrorBg:   "#f0e8e8",

		MdHeading: "#b4637a",
		MdLink:    "#286983",
		MdCode:    "#56949f",

		SyntaxComment:  "#9893a5",
		SyntaxKeyword:  "#907aa9",
		SyntaxFunction: "#ea9d34",
		SyntaxString:   "#56949f",
		SyntaxNumber:   "#ea9d34",
		SyntaxType:     "#286983",
	},

	"monokai": {
		Name: "monokai", Dark: true,
		Accent:  "#a6e22e",
		Success: "#a6e22e",
		Error:   "#f92672",
		Warning: "#e6db74",
		Text:    "#f8f8f2",
		Muted:   "#75715e",
		Dim:     "#49483e",
		System:  "#66d9ef",

		UserMessage:  "#a6e22e",
		AssistantText: "#f8f8f2",

		SelectedBg:    "#49483e",
		UserMessageBg: "#272822",
		ToolSuccessBg: "#1e3a1e",
		ToolErrorBg:   "#3a1e2a",

		MdHeading: "#f92672",
		MdLink:    "#66d9ef",
		MdCode:    "#a6e22e",

		SyntaxComment:  "#75715e",
		SyntaxKeyword:  "#f92672",
		SyntaxFunction: "#a6e22e",
		SyntaxString:   "#e6db74",
		SyntaxNumber:   "#ae81ff",
		SyntaxType:     "#66d9ef",
	},

	"synthwave-84": {
		Name: "synthwave-84", Dark: true,
		Accent:  "#f97e72",
		Success: "#72f1b8",
		Error:   "#fe4450",
		Warning: "#fede5d",
		Text:    "#ffffff",
		Muted:   "#7a7a9e",
		Dim:     "#445588",
		System:  "#36f9f6",

		UserMessage:  "#f97e72",
		AssistantText: "#ffffff",

		SelectedBg:    "#556699",
		UserMessageBg: "#262335",
		ToolSuccessBg: "#1e3a3a",
		ToolErrorBg:   "#3a1e2a",

		MdHeading: "#f97e72",
		MdLink:    "#36f9f6",
		MdCode:    "#72f1b8",

		SyntaxComment:  "#7a7a9e",
		SyntaxKeyword:  "#f97e72",
		SyntaxFunction: "#fede5d",
		SyntaxString:   "#72f1b8",
		SyntaxNumber:   "#ff7edb",
		SyntaxType:     "#36f9f6",
	},

	"material-darker": {
		Name: "material-darker", Dark: true,
		Accent:  "#80cbc4",
		Success: "#c3e88d",
		Error:   "#f07178",
		Warning: "#ffcb6b",
		Text:    "#eeffff",
		Muted:   "#546e7a",
		Dim:     "#3a4a55",
		System:  "#82aaff",

		UserMessage:  "#80cbc4",
		AssistantText: "#eeffff",

		SelectedBg:    "#3a4a55",
		UserMessageBg: "#212121",
		ToolSuccessBg: "#1e3a2a",
		ToolErrorBg:   "#3a1e1e",

		MdHeading: "#f07178",
		MdLink:    "#82aaff",
		MdCode:    "#c3e88d",

		SyntaxComment:  "#546e7a",
		SyntaxKeyword:  "#c792ea",
		SyntaxFunction: "#82aaff",
		SyntaxString:   "#c3e88d",
		SyntaxNumber:   "#f78c6c",
		SyntaxType:     "#80cbc4",
	},

	// ========== LIGHT THEMES ==========

	"material-light": {
		Name: "material-light", Dark: false,
		Accent:  "#39adb5",
		Success: "#91b859",
		Error:   "#e53935",
		Warning: "#ffb62c",
		Text:    "#90a7b1",
		Muted:   "#a0b0c0",
		Dim:     "#c0d0d8",
		System:  "#6182b8",

		UserMessage:  "#39adb5",
		AssistantText: "#90a7b1",

		SelectedBg:    "#d0dce8",
		UserMessageBg: "#fafafa",
		ToolSuccessBg: "#e8f0e8",
		ToolErrorBg:   "#f0e8e8",

		MdHeading: "#e53935",
		MdLink:    "#6182b8",
		MdCode:    "#91b859",

		SyntaxComment:  "#a0b0c0",
		SyntaxKeyword:  "#7c4dff",
		SyntaxFunction: "#6182b8",
		SyntaxString:   "#91b859",
		SyntaxNumber:   "#e67e22",
		SyntaxType:     "#39adb5",
	},
}
