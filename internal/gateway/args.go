package gateway

import (
	"encoding/json"
	"strings"
)

// --- arg helpers ---

func str(v any) string {
	s, _ := v.(string)
	return s
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case string:
		t = strings.TrimSpace(t)
		if t == "" {
			return nil
		}
		// Some MCP clients serialize array-typed params as a JSON string
		// literal (e.g. attachments/to arriving as `["a@x","b@x"]` instead
		// of a real array). Parse that back into the list it was meant to
		// be — otherwise the whole JSON blob is treated as one path/address
		// and every recipient/upload lookup fails (bit Felix twice).
		if strings.HasPrefix(t, "[") {
			var arr []string
			if err := json.Unmarshal([]byte(t), &arr); err == nil && arr != nil {
				out := make([]string, 0, len(arr))
				for _, p := range arr {
					if p = strings.TrimSpace(p); p != "" {
						out = append(out, p)
					}
				}
				return out
			}
		}
		parts := strings.Split(t, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	}
	return nil
}
