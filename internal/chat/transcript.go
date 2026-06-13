package chat

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

// TranscriptResult summarizes the outcome of a headless run read back from its
// --out JSONL transcript: the final assistant message text and whether the run
// recorded a terminal error. It is the bridge a subprocess host uses to turn a
// child run's transcript into an agent_done pointer (status + preview) or a cron
// Notice (the result text), without re-streaming the conversation.
type TranscriptResult struct {
	// FinalText is the text of the last assistant_message event (the run's
	// concluding reply). Empty when the run produced no assistant message.
	FinalText string
	// Errored is true when the transcript contains an error event OR its end
	// line records a non-ok status — either marks the run as failed.
	Errored bool
}

// ReadTranscript scans a --out JSONL audit log and extracts the final assistant
// text and error status (see TranscriptResult). A missing/unreadable file
// yields a zero result (no final text, not errored) rather than an error: the
// caller treats "no transcript" as "no preview available", and the transcript
// pointer in the notification is the durable guarantee regardless.
//
// It reads line-by-line so a large transcript does not load whole into memory;
// only the latest assistant_message text is retained.
func ReadTranscript(path string) TranscriptResult {
	f, err := os.Open(path)
	if err != nil {
		return TranscriptResult{}
	}
	defer f.Close()

	var res TranscriptResult
	sc := bufio.NewScanner(f)
	// A single assistant_message can be large (a long final reply); raise the
	// scanner's max line length well above the 64 KiB default so it is not split.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Kind   string `json:"kind"`
			Text   string `json:"text"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // skip malformed lines rather than aborting the whole read
		}
		switch rec.Kind {
		case "assistant_message":
			res.FinalText = rec.Text
		case "error":
			res.Errored = true
		case "end":
			if rec.Status != "" && rec.Status != "ok" {
				res.Errored = true
			}
		}
	}
	res.FinalText = strings.TrimSpace(res.FinalText)
	return res
}
