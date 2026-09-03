//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/strutil"
)

const (
	defaultNoticeListLimit = 10
	maxNoticeListLimit     = 100
	defaultNoticeReadLimit = 8 << 10
	maxNoticeReadLimit     = 32 << 10
	noticePreviewRunes     = 240
)

func newInboxCommand() *cobra.Command {
	var stateRoot, workDir, target string
	var here bool
	c := &cobra.Command{
		Use:   "inbox",
		Short: "Inspect durable inbox notices without loading them all at once",
		Args:  cobra.NoArgs,
	}
	c.PersistentFlags().StringVar(&stateRoot, "state", "", "Override the shell3 state directory")
	c.PersistentFlags().StringVar(&workDir, "workdir", "", "Runtime working directory (default ~/.shell3/workdir)")
	c.PersistentFlags().BoolVar(&here, "here", false, "Use the current directory's state")
	c.PersistentFlags().StringVar(&target, "to", "main", "Notice destination")
	root := func(cmd *cobra.Command) (string, error) {
		return resolveStateRoot(cmd, stateRoot, workDir, here)
	}
	c.AddCommand(newInboxListCommand(root, &target))
	c.AddCommand(newInboxReadCommand(root, &target))
	c.AddCommand(newInboxArchiveCommand(root, &target))
	return c
}

type inboxRootResolver func(*cobra.Command) (string, error)

func newInboxArchiveCommand(resolveRoot inboxRootResolver, target *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "archive <ids> [ids...]",
		Short: "Archive fully read notices",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			stateRoot, err := resolveRoot(cmd)
			if err != nil {
				return err
			}
			var ids []string
			for _, arg := range args {
				for _, id := range strings.Split(arg, ",") {
					id = strings.TrimSpace(id)
					if id == "" {
						return errors.New("inbox archive: empty message id")
					}
					ids = append(ids, id)
				}
			}
			if err := (inbox.Store{Root: stateRoot}).ArchiveRead(*target, ids); err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
				Archived []string `json:"archived"`
			}{Archived: ids})
		},
	}
	return c
}

type noticeListItem struct {
	ID        string             `json:"id"`
	Status    inbox.NoticeStatus `json:"status"`
	Source    string             `json:"source"`
	Event     string             `json:"event"`
	Created   string             `json:"created"`
	BodyBytes int                `json:"body_bytes"`
	Preview   string             `json:"preview"`
}

type noticeListOutput struct {
	Target     string           `json:"target"`
	Status     string           `json:"status"`
	Offset     int              `json:"offset"`
	Limit      int              `json:"limit"`
	Total      int              `json:"total"`
	NextOffset *int             `json:"next_offset,omitempty"`
	Notices    []noticeListItem `json:"notices"`
}

func newInboxListCommand(resolveRoot inboxRootResolver, target *string) *cobra.Command {
	var status string
	var offset, limit int
	c := &cobra.Command{
		Use:   "list",
		Short: "List a bounded page of notice metadata and previews",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stateRoot, err := resolveRoot(cmd)
			if err != nil {
				return err
			}
			if offset < 0 {
				return errors.New("inbox list: offset must be non-negative")
			}
			if limit < 1 || limit > maxNoticeListLimit {
				return fmt.Errorf("inbox list: limit must be between 1 and %d", maxNoticeListLimit)
			}
			page, total, err := (inbox.Store{Root: stateRoot}).List(*target, inbox.NoticeStatus(status), offset, limit)
			if err != nil {
				return err
			}
			out := noticeListOutput{
				Target: *target, Status: status, Offset: offset, Limit: limit, Total: total,
				Notices: make([]noticeListItem, 0, len(page)),
			}
			for _, notice := range page {
				msg := notice.Message
				out.Notices = append(out.Notices, noticeListItem{
					ID: msg.ID, Status: notice.Status, Source: msg.Source, Event: msg.Event,
					Created: msg.Created.Format("2006-01-02T15:04:05Z07:00"), BodyBytes: len(msg.Body),
					Preview: strutil.Truncate(strings.Join(strings.Fields(msg.Body), " "), noticePreviewRunes),
				})
			}
			if next := offset + len(page); next < total {
				out.NextOffset = &next
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
	c.Flags().StringVar(&status, "status", string(inbox.StatusPending), "Notice status: pending, new, processing, archived, or all")
	c.Flags().IntVar(&offset, "offset", 0, "Zero-based notice offset")
	c.Flags().IntVar(&limit, "limit", defaultNoticeListLimit, "Maximum notices to return")
	return c
}

type noticeReadOutput struct {
	ID          string             `json:"id"`
	Status      inbox.NoticeStatus `json:"status"`
	Target      string             `json:"target"`
	Source      string             `json:"source"`
	Event       string             `json:"event"`
	Created     string             `json:"created"`
	Correlation string             `json:"correlation,omitempty"`
	Offset      int                `json:"offset"`
	BodyBytes   int                `json:"body_bytes"`
	NextOffset  *int               `json:"next_offset,omitempty"`
	Body        string             `json:"body"`
}

func newInboxReadCommand(resolveRoot inboxRootResolver, target *string) *cobra.Command {
	var offset, limit int
	c := &cobra.Command{
		Use:   "read <message-id>",
		Short: "Read one bounded chunk of a notice body",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			stateRoot, err := resolveRoot(cmd)
			if err != nil {
				return err
			}
			if offset < 0 {
				return errors.New("inbox read: offset must be non-negative")
			}
			if limit < 1 || limit > maxNoticeReadLimit {
				return fmt.Errorf("inbox read: limit must be between 1 and %d bytes", maxNoticeReadLimit)
			}
			store := inbox.Store{Root: stateRoot}
			notice, err := store.Read(*target, args[0])
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inbox read: %s not found", args[0])
			}
			if err != nil {
				return err
			}
			if notice.Status == inbox.StatusNew || notice.Status == inbox.StatusProcessing {
				expected, err := store.ReadOffset(*target, args[0])
				if err != nil {
					return err
				}
				if offset > expected {
					return fmt.Errorf("inbox read: continue at offset %d", expected)
				}
			}
			body, next, err := boundedNoticeBody(notice.Message.Body, offset, limit)
			if err != nil {
				return err
			}
			msg := notice.Message
			out := noticeReadOutput{
				ID: msg.ID, Status: notice.Status, Target: msg.To, Source: msg.Source, Event: msg.Event,
				Created: msg.Created.Format("2006-01-02T15:04:05Z07:00"), Correlation: msg.Correlation,
				Offset: offset, BodyBytes: len(msg.Body), NextOffset: next, Body: body,
			}
			var encoded bytes.Buffer
			if err := json.NewEncoder(&encoded).Encode(out); err != nil {
				return err
			}
			if _, err := cmd.OutOrStdout().Write(encoded.Bytes()); err != nil {
				return err
			}
			if notice.Status == inbox.StatusNew || notice.Status == inbox.StatusProcessing {
				end := len(msg.Body)
				if next != nil {
					end = *next
				}
				if err := store.RecordRead(*target, msg.ID, offset, end, len(msg.Body)); err != nil {
					return fmt.Errorf("inbox read: record progress: %w", err)
				}
			}
			return nil
		},
	}
	c.Flags().IntVar(&offset, "offset", 0, "Byte offset at a UTF-8 boundary")
	c.Flags().IntVar(&limit, "limit", defaultNoticeReadLimit, "Maximum body bytes to return")
	return c
}

func boundedNoticeBody(body string, offset, limit int) (string, *int, error) {
	if offset > len(body) {
		return "", nil, fmt.Errorf("inbox read: offset %d exceeds body size %d", offset, len(body))
	}
	if offset < len(body) && !utf8.RuneStart(body[offset]) {
		return "", nil, errors.New("inbox read: offset must be at a UTF-8 boundary")
	}
	end := min(len(body), offset+limit)
	for end > offset && end < len(body) && !utf8.RuneStart(body[end]) {
		end--
	}
	if end == offset && end < len(body) {
		_, size := utf8.DecodeRuneInString(body[offset:])
		end += size
	}
	if end < len(body) {
		next := end
		return body[offset:end], &next, nil
	}
	return body[offset:end], nil, nil
}
