//go:build unix

package telegram

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// compile-time proof the console client satisfies the Bot's transport surface.
var _ tgClient = (*ConsoleClient)(nil)

func TestConsoleOutboundFormatting(t *testing.T) {
	var out bytes.Buffer
	c := NewConsoleClient(strings.NewReader(""), &out, ConsoleChatID)
	ctx := context.Background()

	id1, _ := c.Send(ctx, ConsoleChatID, "hello")
	id2, _ := c.SendHTML(ctx, ConsoleChatID, "<b>bold</b>")
	id3, _ := c.SendReply(ctx, ConsoleChatID, "threaded", id1)
	id4, _ := c.SendHTMLReply(ctx, ConsoleChatID, "<i>x</i>", id2)

	// Monotonic, shared id space.
	if id1 != "1" || id2 != "2" || id3 != "3" || id4 != "4" {
		t.Fatalf("ids not monotonic: %s %s %s %s", id1, id2, id3, id4)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	want := []string{
		"[#1] hello",
		"[#2] <b>bold</b>", // HTML printed raw, unrendered
		"[#3 ↩#1] threaded",
		"[#4 ↩#2] <i>x</i>",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), out.String())
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

func TestConsoleMediaMarkers(t *testing.T) {
	var out bytes.Buffer
	c := NewConsoleClient(strings.NewReader(""), &out, ConsoleChatID)
	ctx := context.Background()

	_ = c.SendPhoto(ctx, ConsoleChatID, "cat.png", []byte("x"), "a cat")
	_ = c.SendVoice(ctx, ConsoleChatID, []byte("x"), "spoken")
	_ = c.EditPlain(ctx, ConsoleChatID, "7", "edited")

	s := out.String()
	for _, want := range []string{
		"[media photo cat.png] a cat",
		"[media voice] spoken",
		"[edit #7] edited",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
}

func TestConsoleInboundParsing(t *testing.T) {
	in := strings.NewReader("hello world\n\n@3 follow up\n/jobs\n@notanint literal\n")
	c := NewConsoleClient(in, &bytes.Buffer{}, ConsoleChatID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := c.Updates(ctx)

	got := drainMsgs(t, ch, 4)

	// blank line skipped: 4 messages, not 5.
	if got[0].Text != "hello world" || got[0].ReplyToID != "" || got[0].ChatID != ConsoleChatID {
		t.Errorf("msg0 = %+v", got[0])
	}
	if got[1].Text != "follow up" || got[1].ReplyToID != "3" {
		t.Errorf("msg1 (@id reply) = %+v", got[1])
	}
	if got[2].Text != "/jobs" || got[2].ReplyToID != "" {
		t.Errorf("msg2 (command) = %+v", got[2])
	}
	// Ids are opaque strings now, so "@notanint …" is a reply to id "notanint".
	if got[3].Text != "literal" || got[3].ReplyToID != "notanint" {
		t.Errorf("msg3 (string @id) = %+v", got[3])
	}
	// ids are assigned to every inbound message.
	if got[0].ID == "" || got[1].ID == got[0].ID || got[2].ID == got[1].ID {
		t.Errorf("inbound ids not distinct: %s %s %s", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestConsoleEOFClosesChannel(t *testing.T) {
	c := NewConsoleClient(strings.NewReader("one\n"), &bytes.Buffer{}, ConsoleChatID)
	ch := c.Updates(context.Background())
	if m := <-ch; m.Text != "one" {
		t.Fatalf("first msg = %+v", m)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel closed after EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed on EOF")
	}
}

// drainMsgs reads exactly n messages off ch or fails on timeout.
func drainMsgs(t *testing.T, ch <-chan Msg, n int) []Msg {
	t.Helper()
	var got []Msg
	deadline := time.After(2 * time.Second)
	for len(got) < n {
		select {
		case m := <-ch:
			got = append(got, m)
		case <-deadline:
			t.Fatalf("only got %d/%d messages", len(got), n)
		}
	}
	return got
}

// A silent send is visible in the console rendering as a 🔕 tag.
func TestConsole_SilentTag(t *testing.T) {
	var out bytes.Buffer
	c := NewConsoleClient(strings.NewReader(""), &out, ConsoleChatID)
	ctx := context.Background()

	if _, err := c.Send(ctx, ConsoleChatID, "hushed", SendOpt{Silent: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "🔕") {
		t.Fatalf("silent send missing 🔕 tag: %s", out.String())
	}
	out.Reset()
	if _, err := c.Send(ctx, ConsoleChatID, "loud"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "🔕") {
		t.Fatalf("plain send carries 🔕 tag: %s", out.String())
	}
}

// "#<chatid> text" drives another room, so the whole multi-room loop is
// exercisable by hand with no credentials. The room counts as a group, which
// means the line must address the bot — same rule as a live group.
func TestConsoleParseLineRoomPrefix(t *testing.T) {
	c := NewConsoleClient(strings.NewReader(""), io.Discard, ConsoleChatID)
	m := c.parseLine("#-100 @shell3console deploy")
	if m.ChatID != -100 {
		t.Fatalf("ChatID = %d, want -100", m.ChatID)
	}
	if m.ChatType != "supergroup" {
		t.Fatalf("ChatType = %q, want a group (the trigger gate must apply)", m.ChatType)
	}
	if m.Text != "@shell3console deploy" {
		t.Fatalf("Text = %q", m.Text)
	}

	// The default chat stays private: no @mention needed at the console.
	plain := c.parseLine("hello")
	if plain.ChatID != ConsoleChatID || plain.ChatType != "private" {
		t.Fatalf("plain line = chat %d type %q, want the default private chat", plain.ChatID, plain.ChatType)
	}

	// A "#" line that is not "#<id> text" is ordinary text, not a route.
	notARoom := c.parseLine("#nope still text")
	if notARoom.ChatID != ConsoleChatID || notARoom.Text != "#nope still text" {
		t.Fatalf("unparseable room prefix = chat %d text %q, want it left alone", notARoom.ChatID, notARoom.Text)
	}
}
