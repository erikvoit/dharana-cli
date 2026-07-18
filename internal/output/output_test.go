package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteOperationJSONIncludesStableEnvelope(t *testing.T) {
	var buf bytes.Buffer

	if err := WriteOperationJSON(&buf, "work.ready", map[string]string{"value": "ok"}); err != nil {
		t.Fatalf("WriteOperationJSON returned error: %v", err)
	}
	out := buf.String()
	for _, expected := range []string{`"ok": true`, `"operation": "work.ready"`, `"data"`} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %s in output, got %s", expected, out)
		}
	}
}

func TestWriteErrorJSONUsesErrorEnvelope(t *testing.T) {
	var buf bytes.Buffer

	if err := WriteErrorJSON(&buf, NewError("BROKEN", "It broke.")); err != nil {
		t.Fatalf("WriteErrorJSON returned error: %v", err)
	}
	out := buf.String()
	for _, expected := range []string{`"ok": false`, `"error"`, `"code": "BROKEN"`} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %s in output, got %s", expected, out)
		}
	}
	if strings.Contains(out, `"operation"`) {
		t.Fatalf("error envelope should not include operation, got %s", out)
	}
}
