package llm

import (
	"reflect"
	"testing"
)

func TestMessageHasNoContentParts(t *testing.T) {
	// read_media was the only producer of content parts. If this field comes
	// back, so did a perception path inside the harness — which is what BYOV
	// exists to keep out.
	if _, ok := reflect.TypeOf(Message{}).FieldByName("ContentParts"); ok {
		t.Fatal("Message.ContentParts survived; nothing produces parts any more")
	}
}
