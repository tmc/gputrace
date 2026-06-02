//go:build darwin

package cmd

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"sort"
	"strconv"
	"strings"
)

type xctraceIntervalRow struct {
	StartNs         uint64 `json:"start_ns"`
	DurationNs      uint64 `json:"duration_ns"`
	Process         string `json:"process"`
	Label           string `json:"label,omitempty"`
	CommandBufferID uint64 `json:"command_buffer_id,omitempty"`
	EncoderID       uint64 `json:"encoder_id,omitempty"`
}

func writeXctraceIntervalRowsJSON(path string, rows []xctraceIntervalRow) error {
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func parseXctraceGPUIntervalsXML(path, processName string, maxRows int) ([]xctraceIntervalRow, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	values := map[string]string{}
	rows := []xctraceIntervalRow{}
	rowsRead := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, rowsRead, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "row" {
			continue
		}
		fields, err := parseXctraceRow(decoder, values)
		if err != nil {
			return nil, rowsRead, err
		}
		rowsRead++
		row, ok := xctraceIntervalFromFields(fields)
		if !ok {
			continue
		}
		if processName != "*" && !strings.Contains(row.Process, processName) {
			continue
		}
		rows = append(rows, row)
		if maxRows > 0 && len(rows) >= maxRows {
			break
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].StartNs < rows[j].StartNs
	})
	return rows, rowsRead, nil
}

func parseXctraceRow(decoder *xml.Decoder, values map[string]string) ([]string, error) {
	fields := []string{}
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			value, err := parseXctraceField(decoder, t, values)
			if err != nil {
				return nil, err
			}
			fields = append(fields, value)
		case xml.EndElement:
			if t.Name.Local == "row" {
				return fields, nil
			}
		}
	}
}

func parseXctraceField(decoder *xml.Decoder, start xml.StartElement, values map[string]string) (string, error) {
	if ref := xmlAttr(start, "ref"); ref != "" {
		if err := skipXMLToEnd(decoder); err != nil {
			return "", err
		}
		return values[ref], nil
	}
	value := xmlAttr(start, "fmt")
	var text strings.Builder
	depth := 1
	hadNested := false
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return "", err
		}
		switch t := token.(type) {
		case xml.CharData:
			text.Write([]byte(t))
		case xml.StartElement:
			hadNested = true
			depth++
			if nestedFmt := xmlAttr(t, "fmt"); nestedFmt != "" {
				if nestedID := xmlAttr(t, "id"); nestedID != "" {
					values[nestedID] = nestedFmt
				}
				if value == "" {
					value = nestedFmt
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	textValue := strings.TrimSpace(text.String())
	if textValue != "" && !hadNested {
		value = textValue
	} else if value == "" {
		value = textValue
	}
	if id := xmlAttr(start, "id"); id != "" {
		values[id] = value
	}
	return value, nil
}

func skipXMLToEnd(decoder *xml.Decoder) error {
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

func xctraceIntervalFromFields(fields []string) (xctraceIntervalRow, bool) {
	if len(fields) < 18 {
		return xctraceIntervalRow{}, false
	}
	startNs, ok := parseUnsignedXctraceValue(fields[0])
	if !ok {
		return xctraceIntervalRow{}, false
	}
	durationNs, ok := parseUnsignedXctraceValue(fields[1])
	if !ok || durationNs == 0 {
		return xctraceIntervalRow{}, false
	}
	cbID, _ := parseUnsignedXctraceValue(fields[15])
	encoderID, _ := parseUnsignedXctraceValue(fields[16])
	return xctraceIntervalRow{
		StartNs:         startNs,
		DurationNs:      durationNs,
		Process:         fields[10],
		Label:           fields[6],
		CommandBufferID: cbID,
		EncoderID:       encoderID,
	}, true
}

func parseUnsignedXctraceValue(value string) (uint64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	base := 10
	if strings.HasPrefix(value, "0x") {
		base = 16
		value = strings.TrimPrefix(value, "0x")
	}
	clean := strings.NewReplacer("'", "", ",", "", " ", "").Replace(value)
	n, err := strconv.ParseUint(clean, base, 64)
	return n, err == nil
}

func xmlAttr(start xml.StartElement, name string) string {
	for _, attr := range start.Attr {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}
