package llm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMessageHasNoContentParts(t *testing.T) {
	if _, ok := reflect.TypeOf(Message{}).FieldByName("ContentParts"); ok {
		t.Fatal("Message.ContentParts survived; nothing produces parts any more")
	}
}

func TestMessageOperatorContentIsNotSerialized(t *testing.T) {
	raw, err := json.Marshal(Message{
		Role:            RoleUser,
		Content:         "visible provider content",
		OperatorContent: "ephemeral authorization provenance",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "ephemeral authorization provenance") || strings.Contains(string(raw), "OperatorContent") {
		t.Fatalf("operator provenance leaked into serialized message: %s", raw)
	}
}
