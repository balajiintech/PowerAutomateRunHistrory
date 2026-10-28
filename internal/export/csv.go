package export

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

var runHeaders = []string{
	"Run start time",
	"Run end time",
	"Run ID",
	"Run status",
	"Run error message",
	"Run link",
}

// Cell is one action, or the run trigger, on a single run.
type Cell struct {
	Status string
	Method string
	Input  json.RawMessage
	Output json.RawMessage
	Start  time.Time
	End    time.Time
}

// Row is one flow run.
type Row struct {
	Start   time.Time
	End     time.Time
	ID      string
	Status  string
	Error   json.RawMessage
	Link    string
	Actions map[string]Cell
}

// FileName matches the Power Automate portal download name.
// Status is lowercased. at is formatted in UTC as yyyymmddthhmmssz.
func FileName(flowID, status string, at time.Time) string {
	stamp := at.UTC().Format("20060102t150405") + "z"
	return fmt.Sprintf("flow-%s-status eq '%s'-%s.csv", flowID, strings.ToLower(status), stamp)
}

// RunLink is the maker portal URL for one run.
func RunLink(environment, flowID, runID string) string {
	return fmt.Sprintf("https://make.powerautomate.com/environments/%s/flows/%s/runs/%s", environment, flowID, runID)
}

// FormatTime renders a portal timestamp. A zero time is an empty cell.
func FormatTime(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("01/02/2006 15:04:05")
}

// Headers is the portal header row for the given column names.
// The column named Trigger uses "Trigger status" rather than "Trigger - status".
func Headers(columns []string) []string {
	headers := make([]string, 0, len(runHeaders)+6*len(columns))
	headers = append(headers, runHeaders...)
	for _, name := range columns {
		headers = append(headers, actionHeaders(name)...)
	}
	return headers
}

func actionHeaders(name string) []string {
	if name == "Trigger" {
		return []string{
			"Trigger status",
			"Trigger method",
			"Trigger input",
			"Trigger output",
			"Trigger start time",
			"Trigger end time",
		}
	}
	return []string{
		name + " - status",
		name + " - method",
		name + " - input",
		name + " - output",
		name + " - start time",
		name + " - end time",
	}
}

// Write writes a portal-style run history CSV, including the UTF-8 BOM and sep=, line.
func Write(w io.Writer, columns []string, rows []Row, loc *time.Location) error {
	if loc == nil {
		loc = time.Local
	}
	if _, err := io.WriteString(w, "\ufeffsep=,\r\n"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	if err := cw.Write(Headers(columns)); err != nil {
		return err
	}
	for _, row := range rows {
		record := []string{
			FormatTime(row.Start, loc),
			FormatTime(row.End, loc),
			row.ID,
			row.Status,
			FormatValue(row.Error),
			row.Link,
		}
		for _, name := range columns {
			cell := row.Actions[name]
			record = append(record,
				cell.Status,
				cell.Method,
				FormatValue(cell.Input),
				FormatValue(cell.Output),
				FormatTime(cell.Start, loc),
				FormatTime(cell.End, loc),
			)
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// FormatValue pretty-prints JSON objects and arrays with CRLF and two-space indent.
// Numbers, booleans, and strings are written as raw scalar text. Null and empty
// payloads are empty cells. Non-JSON text is kept as-is.
func FormatValue(raw []byte) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return string(raw)
	}
	switch t := tok.(type) {
	case json.Delim:
		if t != '{' && t != '[' {
			return string(raw)
		}
		var buf bytes.Buffer
		if err := writePretty(&buf, dec, t, 0); err != nil {
			return string(raw)
		}
		return buf.String()
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		return string(raw)
	}
}

func writePretty(buf *bytes.Buffer, dec *json.Decoder, open json.Delim, indent int) error {
	buf.WriteByte(byte(open))
	closeDelim := json.Delim('}')
	if open == '[' {
		closeDelim = ']'
	}
	first := true
	wrote := false
	for dec.More() {
		wrote = true
		if !first {
			buf.WriteByte(',')
		}
		first = false
		buf.WriteString("\r\n")
		buf.WriteString(strings.Repeat("  ", indent+1))
		if open == '{' {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("expected object key")
			}
			buf.WriteString(jsonString(key))
			buf.WriteString(": ")
		}
		if err := writeValue(buf, dec, indent+1); err != nil {
			return err
		}
	}
	end, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := end.(json.Delim)
	if !ok || d != closeDelim {
		return fmt.Errorf("expected closing delimiter")
	}
	if wrote {
		buf.WriteString("\r\n")
		buf.WriteString(strings.Repeat("  ", indent))
	}
	buf.WriteByte(byte(closeDelim))
	return nil
}

func writeValue(buf *bytes.Buffer, dec *json.Decoder, indent int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t != '{' && t != '[' {
			return fmt.Errorf("unexpected delimiter")
		}
		return writePretty(buf, dec, t, indent)
	case string:
		buf.WriteString(jsonString(t))
		return nil
	case json.Number:
		buf.WriteString(t.String())
		return nil
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case nil:
		buf.WriteString("null")
		return nil
	default:
		return fmt.Errorf("unexpected token")
	}
}

func jsonString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		b, _ := json.Marshal(s)
		return string(b)
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}
