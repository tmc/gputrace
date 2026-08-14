package counter

import (
	"strconv"
	"strings"
)

// PipelineCompilerRemark is one searchable projection of a compiler Remarks
// YAML document. The original Remarks string remains the authoritative source.
type PipelineCompilerRemark struct {
	Index        int    `json:"index"`
	Kind         string `json:"kind,omitempty"`
	Pass         string `json:"pass,omitempty"`
	Name         string `json:"name,omitempty"`
	Function     string `json:"function,omitempty"`
	SourceFile   string `json:"source_file,omitempty"`
	SourceLine   *int   `json:"source_line,omitempty"`
	SourceColumn *int   `json:"source_column,omitempty"`
	ParseStatus  string `json:"parse_status"`
}

// ParseCompilerRemarks projects stable header fields from compiler Remarks
// YAML. It does not interpret pass-specific arguments or replace the raw text.
func ParseCompilerRemarks(raw string) []PipelineCompilerRemark {
	documents := remarkDocuments(raw)
	remarks := make([]PipelineCompilerRemark, 0, len(documents))
	for index, document := range documents {
		remark := PipelineCompilerRemark{
			Index: index, Kind: remarkKind(document),
			Pass:        remarkField(document, "Pass"),
			Name:        remarkField(document, "Name"),
			Function:    remarkField(document, "Function"),
			ParseStatus: "no_source_location",
		}
		if remark.Kind == "" || remark.Pass == "" || remark.Name == "" || remark.Function == "" {
			remark.ParseStatus = "malformed"
			remarks = append(remarks, remark)
			continue
		}
		location, present, ok := remarkDebugLocation(document)
		if present && !ok {
			remark.ParseStatus = "malformed"
		} else if present {
			remark.SourceFile = location.file
			line, column := location.line, location.column
			remark.SourceLine = &line
			remark.SourceColumn = &column
			if line == 0 {
				remark.ParseStatus = "unresolved_source_location"
			} else {
				remark.ParseStatus = "complete"
			}
		}
		remarks = append(remarks, remark)
	}
	return remarks
}

func remarkDocuments(raw string) []string {
	var documents []string
	start := -1
	for offset := 0; offset < len(raw); {
		lineEnd := strings.IndexByte(raw[offset:], '\n')
		if lineEnd < 0 {
			lineEnd = len(raw) - offset
		}
		line := raw[offset : offset+lineEnd]
		if strings.HasPrefix(line, "--- !") {
			if start >= 0 {
				documents = append(documents, raw[start:offset])
			}
			start = offset
		}
		offset += lineEnd
		if offset < len(raw) {
			offset++
		}
	}
	if start >= 0 {
		documents = append(documents, raw[start:])
	} else if strings.TrimSpace(raw) != "" {
		documents = append(documents, raw)
	}
	return documents
}

func remarkKind(document string) string {
	line, _, _ := strings.Cut(document, "\n")
	kind := strings.TrimSpace(strings.TrimPrefix(line, "--- !"))
	if kind == line || strings.ContainsAny(kind, " \t") {
		return ""
	}
	return kind
}

func remarkField(document, name string) string {
	prefix := name + ":"
	for _, line := range strings.Split(document, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

type remarkLocation struct {
	file         string
	line, column int
}

func remarkDebugLocation(document string) (remarkLocation, bool, bool) {
	const key = "DebugLoc:"
	start := topLevelRemarkField(document, key)
	if start < 0 {
		return remarkLocation{}, false, true
	}
	for start < len(document) && isRemarkSpace(document[start]) {
		start++
	}
	if start >= len(document) || document[start] != '{' {
		return remarkLocation{}, true, false
	}
	end, ok := matchingFlowBrace(document, start)
	if !ok {
		return remarkLocation{}, true, false
	}
	fields, ok := flowFields(document[start+1 : end])
	if !ok {
		return remarkLocation{}, true, false
	}
	file, fileOK := flowString(fields["File"])
	line, lineErr := strconv.Atoi(strings.TrimSpace(fields["Line"]))
	column, columnErr := strconv.Atoi(strings.TrimSpace(fields["Column"]))
	if !fileOK || file == "" || lineErr != nil || line < 0 || columnErr != nil || column < 0 {
		return remarkLocation{}, true, false
	}
	return remarkLocation{file: file, line: line, column: column}, true, true
}

func topLevelRemarkField(document, key string) int {
	for offset := 0; offset < len(document); {
		lineEnd := strings.IndexByte(document[offset:], '\n')
		if lineEnd < 0 {
			lineEnd = len(document) - offset
		}
		if strings.HasPrefix(document[offset:offset+lineEnd], key) {
			return offset + len(key)
		}
		offset += lineEnd
		if offset < len(document) {
			offset++
		}
	}
	return -1
}

func matchingFlowBrace(text string, start int) (int, bool) {
	depth := 0
	var quote byte
	for i := start; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if quote == '\'' && c == '\'' && i+1 < len(text) && text[i+1] == '\'' {
				i++
				continue
			}
			if quote == '"' && c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
			if depth < 0 {
				return 0, false
			}
		}
	}
	return 0, false
}

func flowFields(body string) (map[string]string, bool) {
	parts, ok := splitFlow(body, ',')
	if !ok {
		return nil, false
	}
	fields := make(map[string]string, len(parts))
	for _, part := range parts {
		pair, ok := splitFlow(part, ':')
		if !ok || len(pair) != 2 {
			return nil, false
		}
		key := strings.TrimSpace(pair[0])
		if key == "" {
			return nil, false
		}
		fields[key] = strings.TrimSpace(pair[1])
	}
	return fields, true
}

func splitFlow(text string, separator byte) ([]string, bool) {
	var parts []string
	start := 0
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if quote == '\'' && c == '\'' && i+1 < len(text) && text[i+1] == '\'' {
				i++
				continue
			}
			if quote == '"' && c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == separator {
			parts = append(parts, text[start:i])
			start = i + 1
			if separator == ':' {
				parts = append(parts, text[start:])
				return parts, true
			}
		}
	}
	if quote != 0 {
		return nil, false
	}
	parts = append(parts, text[start:])
	return parts, true
}

func flowString(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return "", false
	}
	switch value[0] {
	case '\'':
		if value[len(value)-1] != '\'' {
			return "", false
		}
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), true
	case '"':
		decoded, err := strconv.Unquote(value)
		return decoded, err == nil
	default:
		return value, true
	}
}

func isRemarkSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}
