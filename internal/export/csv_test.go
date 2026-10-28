package export

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFileName(t *testing.T) {
	at := time.Date(2026, 10, 28, 14, 20, 26, 0, time.UTC)
	got := FileName("e2738c2b-3425-453b-a705-75049ce8c588", "Failed", at)
	want := "flow-e2738c2b-3425-453b-a705-75049ce8c588-status eq 'failed'-20261028t142026z.csv"
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestFormatTime(t *testing.T) {
	loc := time.FixedZone("UTC-4", -4*60*60)
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{name: "zero", want: ""},
		{
			name: "converted",
			in:   time.Date(2026, 10, 27, 23, 56, 32, 0, time.UTC),
			want: "10/27/2026 19:56:32",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatTime(tc.in, loc); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", want: ""},
		{name: "null", in: "null", want: ""},
		{name: "number", in: "3134", want: "3134"},
		{name: "bool", in: "true", want: "true"},
		{name: "string", in: `"hello"`, want: "hello"},
		{name: "text", in: "not json", want: "not json"},
		{
			name: "object",
			in:   `{"code":"ActionFailed","message":"An action failed."}`,
			want: "{\r\n  \"code\": \"ActionFailed\",\r\n  \"message\": \"An action failed.\"\r\n}",
		},
		{
			name: "html",
			in:   `{"note":"x<y"}`,
			want: "{\r\n  \"note\": \"x<y\"\r\n}",
		},
		{
			name: "array",
			in:   `[1,{"b":2}]`,
			want: "[\r\n  1,\r\n  {\r\n    \"b\": 2\r\n  }\r\n]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatValue([]byte(tc.in)); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestHeaders(t *testing.T) {
	got := Headers([]string{"Trigger", "Compose"})
	want := []string{
		"Run start time",
		"Run end time",
		"Run ID",
		"Run status",
		"Run error message",
		"Run link",
		"Trigger status",
		"Trigger method",
		"Trigger input",
		"Trigger output",
		"Trigger start time",
		"Trigger end time",
		"Compose - status",
		"Compose - method",
		"Compose - input",
		"Compose - output",
		"Compose - start time",
		"Compose - end time",
	}
	if len(got) != len(want) {
		t.Fatalf("len %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("column %d got %q want %q", i, got[i], want[i])
		}
	}
}

func TestWriteRow(t *testing.T) {
	loc := time.FixedZone("UTC-4", -4*60*60)
	start := time.Date(2026, 10, 27, 23, 56, 32, 0, time.UTC)
	end := time.Date(2026, 10, 27, 23, 58, 29, 0, time.UTC)
	triggerAt := time.Date(2026, 10, 27, 23, 56, 31, 0, time.UTC)
	var buf bytes.Buffer
	err := Write(&buf, []string{"Trigger", "Compose", "Get_file_content"}, []Row{{
		Start:  start,
		End:    end,
		ID:     "run1",
		Status: "Failed",
		Error:  json.RawMessage(`{"code":"ActionFailed","message":"An action failed."}`),
		Link:   RunLink("env", "flow", "run1"),
		Actions: map[string]Cell{
			"Trigger": {
				Status: "Succeeded",
				Input:  json.RawMessage(`{"host":"sharepoint"}`),
				Start:  triggerAt,
				End:    triggerAt,
			},
			"Compose": {
				Status: "Succeeded",
				Input:  json.RawMessage(`3134`),
				Output: json.RawMessage(`3134`),
				Start:  start,
				End:    start,
			},
			"Get_file_content": {
				Status: "Skipped",
				Start:  start,
				End:    start,
			},
		},
	}}, loc)
	if err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	prefix := []byte("\ufeffsep=,\r\n")
	if !bytes.HasPrefix(raw, prefix) {
		t.Fatalf("missing portal prefix: %q", raw[:20])
	}
	if !bytes.Contains(raw, []byte("\r\n")) {
		t.Fatal("expected CRLF")
	}
	reader := csv.NewReader(bytes.NewReader(raw[len(prefix):]))
	header, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	if header[6] != "Trigger status" || header[12] != "Compose - status" {
		t.Fatalf("header %q", header)
	}
	row, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	if row[0] != "10/27/2026 19:56:32" || row[1] != "10/27/2026 19:58:29" || row[2] != "run1" || row[3] != "Failed" {
		t.Fatalf("run cells %q", row[:4])
	}
	const prettyErr = "{\r\n  \"code\": \"ActionFailed\",\r\n  \"message\": \"An action failed.\"\r\n}"
	// csv.Writer quotes the field and doubles embedded quotes, and UseCRLF keeps CRLF.
	if !bytes.Contains(raw, []byte("{\r\n  \"\"code\"\": \"\"ActionFailed\"\"")) {
		t.Fatalf("file missing CRLF error json: %q", raw)
	}
	// encoding/csv drops CR when it folds CRLF inside a quoted field.
	if row[4] != strings.ReplaceAll(prettyErr, "\r\n", "\n") {
		t.Fatalf("error cell %q", row[4])
	}
	if row[5] != "https://make.powerautomate.com/environments/env/flows/flow/runs/run1" {
		t.Fatalf("link %s", row[5])
	}
	if row[6] != "Succeeded" || row[7] != "" || row[8] != "{\n  \"host\": \"sharepoint\"\n}" {
		t.Fatalf("trigger cells %q", row[6:9])
	}
	if row[12] != "Succeeded" || row[13] != "" || row[14] != "3134" || row[15] != "3134" {
		t.Fatalf("compose cells %q", row[12:16])
	}
	if row[18] != "Skipped" || row[19] != "" || row[20] != "" || row[21] != "" {
		t.Fatalf("skipped cells %q", row[18:22])
	}
}
