package runs

import "testing"

func TestParseMessagesLenient(t *testing.T) {
	raw := `{"role":"user","content":"hi"}
not json
{"role":"assistant","content":"yo"}

{"role":"tool","name":"bash","content":"out"}
{"truncated tail`
	msgs := ParseMessages(raw)
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[2].Role != "tool" {
		t.Errorf("roles = %s,%s,%s", msgs[0].Role, msgs[1].Role, msgs[2].Role)
	}
}

func TestDecodeLinesInteriorCorruptionFatal(t *testing.T) {
	type row struct{ N int }
	rows, err := decodeLinesTolerantTail[row]("{\"N\":1}\n{\"N\":2}\n")
	if err != nil || len(rows) != 2 || rows[1].N != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if _, err := decodeLinesTolerantTail[row]("{\"N\":1}\nnope\n{\"N\":2}\n"); err == nil {
		t.Fatal("interior corruption should error")
	}
}

// A malformed FINAL line (a crash mid-append) must be tolerated so resume
// still works; interior corruption stays fatal (asserted above).
func TestDecodeLinesTolerantTail_HalfWrittenTail(t *testing.T) {
	type row struct{ N int }
	rows, err := decodeLinesTolerantTail[row]("{\"N\":1}\n{\"N\":2}\n{\"N\":")
	if err != nil {
		t.Fatalf("half-written tail should not fail the decode: %v", err)
	}
	if len(rows) != 2 || rows[0].N != 1 || rows[1].N != 2 {
		t.Fatalf("rows = %+v, want the two complete lines", rows)
	}
}
