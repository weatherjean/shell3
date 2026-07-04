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

func TestDecodeLinesStrict(t *testing.T) {
	type row struct{ N int }
	rows, err := decodeLines[row]("{\"N\":1}\n{\"N\":2}\n", true)
	if err != nil || len(rows) != 2 || rows[1].N != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if _, err := decodeLines[row]("{\"N\":1}\nnope\n", true); err == nil {
		t.Fatal("strict mode should error on a malformed line")
	}
}
