// Package-level config: TOML configuration files for logprint.
//
// logprint deliberately has no external dependencies. Instead of a TOML
// library, this file provides a small, self-contained parser for the TOML
// subset logprint needs:
//   - comments (#)
//   - tables ([name]) and nested tables ([a.b])
//   - dotted keys (a.b = ...)
//   - values: strings ("..." with escapes, '...' literal), integers,
//     booleans, floats and arrays of values
//
// Not supported are, for example, multi-line strings, inline tables and
// [[array-of-tables]], none of which a printer configuration requires.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// defaultConfig returns the documented default values.
func defaultConfig() config {
	return config{
		host:          "192.168.1.185",
		port:          9100,
		font:          "a",
		indent:        "  ",
		codepage:      "cp858",
		inputEncoding: "utf-8",
		feed:          3,
		// width = 0 is resolved later from the font (48/64).
	}
}

// loadConfigFile reads a TOML file and applies its set keys to the config
// struct. Command-line flags are applied later and take precedence.
func loadConfigFile(c *config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config file: %w", err)
	}
	root, err := parseToml(string(data))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := applyToml(c, root); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// applyToml walks a parsed TOML tree and maps its leaf keys to config
// fields. Table names are ignored; only the last key segment matters, so
// both flat keys and a [printer] section work.
func applyToml(c *config, node map[string]interface{}) error {
	for k, v := range node {
		switch val := v.(type) {
		case map[string]interface{}:
			if err := applyToml(c, val); err != nil {
				return err
			}
		default:
			if err := setField(c, k, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// typeErr reports that a config key has the wrong type.
func typeErr(name, want string) error {
	return fmt.Errorf("key %q must be a %s", name, want)
}

// setField assigns one TOML key/value pair to its config field. Unknown
// keys are ignored so that future keys do not break older binaries.
func setField(c *config, name string, v interface{}) error {
	switch strings.ToLower(strings.ReplaceAll(name, "-", "_")) {
	case "host":
		s, ok := v.(string)
		if !ok {
			return typeErr(name, "string")
		}
		c.host = s
	case "port":
		n, ok := asInt(v)
		if !ok {
			return typeErr(name, "integer")
		}
		c.port = n
	case "follow":
		b, ok := v.(bool)
		if !ok {
			return typeErr(name, "boolean")
		}
		c.follow = b
	case "from_start":
		b, ok := v.(bool)
		if !ok {
			return typeErr(name, "boolean")
		}
		c.fromStart = b
	case "width":
		n, ok := asInt(v)
		if !ok {
			return typeErr(name, "integer")
		}
		c.width = n
	case "font":
		s, ok := v.(string)
		if !ok {
			return typeErr(name, "string")
		}
		c.font = s
	case "indent":
		s, ok := v.(string)
		if !ok {
			return typeErr(name, "string")
		}
		c.indent = s
	case "codepage":
		s, ok := v.(string)
		if !ok {
			return typeErr(name, "string")
		}
		c.codepage = s
	case "input_encoding":
		s, ok := v.(string)
		if !ok {
			return typeErr(name, "string")
		}
		c.inputEncoding = s
	case "timestamps":
		b, ok := v.(bool)
		if !ok {
			return typeErr(name, "boolean")
		}
		c.timestamps = b
	case "no_header":
		b, ok := v.(bool)
		if !ok {
			return typeErr(name, "boolean")
		}
		c.noHeader = b
	case "feed":
		n, ok := asInt(v)
		if !ok {
			return typeErr(name, "integer")
		}
		c.feed = n
	case "cut":
		b, ok := v.(bool)
		if !ok {
			return typeErr(name, "boolean")
		}
		c.cut = b
	case "cut_on_exit":
		b, ok := v.(bool)
		if !ok {
			return typeErr(name, "boolean")
		}
		c.cutOnExit = b
	case "max_lines":
		n, ok := asInt(v)
		if !ok {
			return typeErr(name, "integer")
		}
		c.maxLines = n
	case "feed_only":
		b, ok := v.(bool)
		if !ok {
			return typeErr(name, "boolean")
		}
		c.feedOnly = b
	case "file":
		s, ok := v.(string)
		if !ok {
			return typeErr(name, "string")
		}
		c.file = s
	}
	return nil
}

// asInt coerces an integer-like TOML value (int, int64 or whole float64)
// into an int and reports whether the coercion is possible.
func asInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

// parseToml parses TOML text into nested maps (and arrays), mapping to
// map[string]interface{} / []interface{}. Leaf values are string, int,
// float64 or bool.
func parseToml(data string) (map[string]interface{}, error) {
	root := map[string]interface{}{}
	cur := root
	lines := strings.Split(data, "\n")
	for i, raw := range lines {
		lineNo := i + 1
		line := stripComment(raw)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line[0] == '[' {
			header := strings.TrimSpace(strings.Trim(line, "[]"))
			if strings.HasPrefix(header, "[") { // [[array-of-tables]]
				header = strings.TrimSpace(strings.Trim(header, "[]"))
			}
			t, err := tableFromPath(root, header, lineNo)
			if err != nil {
				return nil, err
			}
			cur = t
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected '=' in TOML", lineNo)
		}
		keyPart := strings.TrimSpace(line[:eq])
		valStr := strings.TrimSpace(line[eq+1:])
		keys := splitDottedKey(keyPart)
		node := cur
		for j, k := range keys {
			k = unquoteKey(k)
			if j == len(keys)-1 {
				v, err := parseValue(valStr, lineNo)
				if err != nil {
					return nil, err
				}
				node[k] = v
				break
			}
			next, ok := node[k].(map[string]interface{})
			if !ok {
				next = map[string]interface{}{}
				node[k] = next
			}
			node = next
		}
	}
	return root, nil
}

// tableFromPath creates a (nested) table for the given dotted path and
// returns it. A path that collides with an existing scalar is an error.
func tableFromPath(root map[string]interface{}, path string, lineNo int) (map[string]interface{}, error) {
	parts := splitDottedKey(path)
	cur := root
	for _, part := range parts {
		part = unquoteKey(part)
		next, ok := cur[part].(map[string]interface{})
		if !ok {
			if _, exists := cur[part]; exists {
				return nil, fmt.Errorf("line %d: table %q collides with a value", lineNo, part)
			}
			next = map[string]interface{}{}
			cur[part] = next
		}
		cur = next
	}
	return cur, nil
}

// splitDottedKey splits a TOML key on dots, but keeps dots that live inside
// quoted key segments.
func splitDottedKey(s string) []string {
	var out []string
	inStr := byte(0)
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr != 0 {
			if inStr == '"' && c == '\\' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inStr = c
			continue
		}
		if c == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// unquoteKey strips matching single or double quotes around a bare key.
func unquoteKey(k string) string {
	k = strings.TrimSpace(k)
	if len(k) >= 2 && k[0] == k[len(k)-1] && (k[0] == '"' || k[0] == '\'') {
		return k[1 : len(k)-1]
	}
	return k
}

// stripComment drops everything from '#' to the end of a line, but keeps
// hashes that live inside quoted strings.
func stripComment(line string) string {
	inStr := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inStr != 0 {
			if inStr == '"' && c == '\\' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inStr = c
			continue
		}
		if c == '#' {
			return line[:i]
		}
	}
	return line
}

// parseValue parses a single TOML value into a string, int, float64, bool
// or []interface{}.
func parseValue(s string, lineNo int) (interface{}, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("line %d: empty value", lineNo)
	}
	switch s[0] {
	case '"':
		return parseBasicString(s, lineNo)
	case '\'':
		return parseLiteralString(s, lineNo)
	case '[':
		return parseArray(s, lineNo)
	case 't', 'f', 'T', 'F':
		switch strings.ToLower(strings.Trim(s, `"'`)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("line %d: invalid value %q", lineNo, s)
	default:
		s = strings.Trim(s, `"'`) // tolerant handling
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return int(i), nil
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, nil
		}
		return nil, fmt.Errorf("line %d: invalid value %q", lineNo, s)
	}
}

// parseBasicString parses a double-quoted TOML string with escape sequences.
func parseBasicString(s string, lineNo int) (string, error) {
	if len(s) < 2 || s[0] != '"' {
		return "", fmt.Errorf("line %d: invalid string", lineNo)
	}
	var sb strings.Builder
	i := 1
	for i < len(s) {
		c := s[i]
		if c == '"' {
			if i+1 < len(s) && s[i+1] != '#' && strings.TrimSpace(s[i+1:]) != "" {
				return "", fmt.Errorf("line %d: unexpected content after string", lineNo)
			}
			return sb.String(), nil
		}
		if c == '\\' {
			i++
			if i >= len(s) {
				break
			}
			switch s[i] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			case 'b':
				sb.WriteByte('\b')
			case 'f':
				sb.WriteByte('\f')
			case 'u':
				if i+4 < len(s) {
					if n, err := strconv.ParseUint(s[i+1:i+5], 16, 16); err == nil {
						sb.WriteRune(rune(n))
						i += 4
					}
				}
			case 'U':
				if i+8 < len(s) {
					if n, err := strconv.ParseUint(s[i+1:i+9], 16, 32); err == nil {
						sb.WriteRune(rune(n))
						i += 8
					}
				}
			default:
				sb.WriteByte(s[i])
			}
			i++
			continue
		}
		sb.WriteByte(c)
		i++
	}
	return "", fmt.Errorf("line %d: unterminated string", lineNo)
}

// parseLiteralString parses a single-quoted TOML literal string (no escapes).
func parseLiteralString(s string, lineNo int) (string, error) {
	if len(s) < 2 || s[0] != '\'' {
		return "", fmt.Errorf("line %d: invalid string", lineNo)
	}
	end := strings.IndexByte(s[1:], '\'')
	if end < 0 {
		return "", fmt.Errorf("line %d: unterminated string", lineNo)
	}
	if rest := strings.TrimSpace(s[end+2:]); rest != "" {
		return "", fmt.Errorf("line %d: unexpected content after string", lineNo)
	}
	return s[1 : 1+end], nil
}

// parseArray parses a TOML array of scalar values into a []interface{}.
func parseArray(s string, lineNo int) ([]interface{}, error) {
	if len(s) < 2 || s[0] != '[' {
		return nil, fmt.Errorf("line %d: invalid array", lineNo)
	}
	end, ok := findArrayEnd(s)
	if !ok {
		return nil, fmt.Errorf("line %d: unterminated array", lineNo)
	}
	if rest := strings.TrimSpace(s[end+1:]); rest != "" {
		return nil, fmt.Errorf("line %d: unexpected content after array", lineNo)
	}
	var out []interface{}
	for _, p := range splitTopLevel(s[1:end], ',') {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := parseValue(p, lineNo)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// findArrayEnd returns the index of the closing ']', aware of quoted strings
// and nested brackets, and reports whether one was found.
func findArrayEnd(s string) (int, bool) {
	depth := 0
	inStr := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr != 0 {
			if inStr == '"' && c == '\\' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inStr = c
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// splitTopLevel splits a TOML value on delim, but only outside quoted strings
// and without splitting across the top level of nested brackets.
func splitTopLevel(s string, delim byte) []string {
	var parts []string
	depth := 0
	inStr := byte(0)
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr != 0 {
			if inStr == '"' && c == '\\' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inStr = c
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		default:
			if c == delim && depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}
