//go:build unix

// Package inbox provides the durable asynchronous ingress shared by sessions
// and wrk runs.
package inbox

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxBodyBytes = 1 << 20

type Message struct {
	ID          string    `json:"id"`
	To          string    `json:"to"`
	Source      string    `json:"source"`
	Trust       string    `json:"trust"`
	Event       string    `json:"event"`
	Created     time.Time `json:"created"`
	Correlation string    `json:"correlation,omitempty"`
	Body        string    `json:"body"`
}

type Request struct {
	To          string
	Source      string
	Event       string
	Correlation string
	Body        string
}

type Receipt struct {
	ID        string `json:"id"`
	Persisted bool   `json:"persisted"`
	Wake      string `json:"wake"`
}

type NoticeStatus string

const (
	StatusNew        NoticeStatus = "new"
	StatusProcessing NoticeStatus = "processing"
	StatusArchived   NoticeStatus = "archived"
	StatusPending    NoticeStatus = "pending"
	StatusAll        NoticeStatus = "all"
)

type Notice struct {
	Message Message
	Status  NoticeStatus
}

type ReadProgress struct {
	ID        string    `json:"id"`
	Offset    int       `json:"offset"`
	BodyBytes int       `json:"body_bytes"`
	Updated   time.Time `json:"updated"`
}

// Delivery is one durably claimed message. Ack removes the claimed copy only
// after its consumer has handled it successfully. An unacked delivery is
// recovered on the next consumer start and may therefore be delivered again.
type Delivery struct {
	Message Message
	path    string
}

type Store struct {
	Root     string
	WakePath string
	Now      func() time.Time
}

// Notify persists one machine-origin message before attempting to wake a live
// process. A wake failure never rolls back an accepted message.
func (s Store) Notify(req Request) (Receipt, error) {
	if err := validateRequest(req); err != nil {
		return Receipt{}, err
	}
	id, err := messageID()
	if err != nil {
		return Receipt{}, fmt.Errorf("inbox: create message id: %w", err)
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	msg := Message{
		ID: id, To: req.To, Source: req.Source, Trust: "machine",
		Event: req.Event, Created: now().UTC(), Correlation: req.Correlation,
		Body: req.Body,
	}
	if err := s.persist(msg); err != nil {
		return Receipt{}, err
	}
	wakePath := s.WakePath
	if wakePath == "" {
		wakePath = filepath.Join(s.Root, "wake.sock")
	}
	wake := "unavailable"
	if err := wakeSocket(wakePath, req.To); err == nil {
		wake = "delivered"
	}
	return Receipt{ID: id, Persisted: true, Wake: wake}, nil
}

func validateRequest(req Request) error {
	if err := validateTarget(req.To); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(req.Source) == "":
		return errors.New("inbox: source is required")
	case strings.TrimSpace(req.Event) == "":
		return errors.New("inbox: event is required")
	case len(req.Body) > maxBodyBytes:
		return fmt.Errorf("inbox: body exceeds %d bytes", maxBodyBytes)
	}
	return nil
}

func (s Store) persist(msg Message) error {
	if strings.TrimSpace(s.Root) == "" {
		return errors.New("inbox: state root is required")
	}
	dir := filepath.Join(s.Root, "inbox", encodeTarget(msg.To), "new")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("inbox: create destination: %w", err)
	}
	data, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		return fmt.Errorf("inbox: encode message: %w", err)
	}
	data = append(data, '\n')
	tmp := filepath.Join(dir, "."+msg.ID+".tmp")
	final := filepath.Join(dir, msg.ID+".json")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("inbox: create message: %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("inbox: write message: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("inbox: sync message: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("inbox: close message: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("inbox: publish message: %w", err)
	}
	ok = true
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Recover returns claims left by a crashed consumer to the new queue.
func (s Store) Recover(target string) error {
	newDir, processingDir, err := s.targetDirs(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		return fmt.Errorf("inbox: create new queue: %w", err)
	}
	entries, err := os.ReadDir(processingDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inbox: read processing queue: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		from := filepath.Join(processingDir, entry.Name())
		to := filepath.Join(newDir, entry.Name())
		if _, err := os.Stat(to); err == nil {
			if err := os.Remove(from); err != nil {
				return fmt.Errorf("inbox: remove duplicate claim: %w", err)
			}
			if err := s.clearReadProgress(target, strings.TrimSuffix(entry.Name(), ".json")); err != nil {
				return err
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inbox: inspect recovered message: %w", err)
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("inbox: recover message: %w", err)
		}
		if err := s.clearReadProgress(target, strings.TrimSuffix(entry.Name(), ".json")); err != nil {
			return err
		}
	}
	if err := syncDir(newDir); err != nil {
		return fmt.Errorf("inbox: sync recovered queue: %w", err)
	}
	if err := syncDir(processingDir); err != nil {
		return fmt.Errorf("inbox: sync processing queue: %w", err)
	}
	return nil
}

// Claim atomically moves the oldest available message into processing.
func (s Store) Claim(target string) (Delivery, bool, error) {
	newDir, processingDir, err := s.targetDirs(target)
	if err != nil {
		return Delivery{}, false, err
	}
	if err := os.MkdirAll(processingDir, 0o700); err != nil {
		return Delivery{}, false, fmt.Errorf("inbox: create processing queue: %w", err)
	}
	entries, err := os.ReadDir(newDir)
	if errors.Is(err, os.ErrNotExist) {
		return Delivery{}, false, nil
	}
	if err != nil {
		return Delivery{}, false, fmt.Errorf("inbox: read new queue: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		from := filepath.Join(newDir, entry.Name())
		claimed := filepath.Join(processingDir, entry.Name())
		if err := os.Rename(from, claimed); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return Delivery{}, false, fmt.Errorf("inbox: claim message: %w", err)
		}
		if err := syncDir(newDir); err != nil {
			return Delivery{}, false, fmt.Errorf("inbox: sync new queue: %w", err)
		}
		if err := syncDir(processingDir); err != nil {
			return Delivery{}, false, fmt.Errorf("inbox: sync claimed queue: %w", err)
		}
		data, err := os.ReadFile(claimed)
		if err != nil {
			return Delivery{}, false, fmt.Errorf("inbox: read claimed message: %w", err)
		}
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			return Delivery{}, false, fmt.Errorf("inbox: decode claimed message: %w", err)
		}
		if msg.To != target || msg.ID+".json" != entry.Name() {
			return Delivery{}, false, errors.New("inbox: claimed message identity mismatch")
		}
		return Delivery{Message: msg, path: claimed}, true, nil
	}
	return Delivery{}, false, nil
}

// Ack permanently removes a successfully handled claim.
func (s Store) Ack(delivery Delivery) error {
	if delivery.path == "" {
		return errors.New("inbox: delivery has no claim path")
	}
	if err := os.Remove(delivery.path); err != nil {
		return fmt.Errorf("inbox: acknowledge message: %w", err)
	}
	if err := syncDir(filepath.Dir(delivery.path)); err != nil {
		return err
	}
	return s.clearReadProgress(delivery.Message.To, delivery.Message.ID)
}

// Archive marks a successfully delivered notice while retaining its complete
// body for later inspection. Workflow consumers use Ack instead because their
// event ledgers are the durable record.
func (s Store) Archive(delivery Delivery) error {
	return s.moveClaim(delivery, StatusArchived, "archive")
}

func (s Store) moveClaim(delivery Delivery, status NoticeStatus, action string) error {
	if delivery.path == "" {
		return errors.New("inbox: delivery has no claim path")
	}
	destinationDir := filepath.Join(filepath.Dir(filepath.Dir(delivery.path)), string(status))
	if err := os.MkdirAll(destinationDir, 0o700); err != nil {
		return fmt.Errorf("inbox: create %s queue: %w", action, err)
	}
	destination := filepath.Join(destinationDir, filepath.Base(delivery.path))
	if err := os.Rename(delivery.path, destination); err != nil {
		return fmt.Errorf("inbox: %s message: %w", action, err)
	}
	if err := syncDir(filepath.Dir(delivery.path)); err != nil {
		return fmt.Errorf("inbox: sync processing queue: %w", err)
	}
	if err := syncDir(destinationDir); err != nil {
		return fmt.Errorf("inbox: sync %s queue: %w", action, err)
	}
	return s.clearReadProgress(delivery.Message.To, delivery.Message.ID)
}

// List returns a stable, oldest-first page of notice metadata. Bodies remain
// on disk and callers decide how much preview text to expose.
func (s Store) List(target string, status NoticeStatus, offset, limit int) ([]Notice, int, error) {
	if offset < 0 || limit < 1 {
		return nil, 0, errors.New("inbox: offset must be non-negative and limit positive")
	}
	dirs, err := s.noticeDirs(target, status)
	if err != nil {
		return nil, 0, err
	}
	var notices []Notice
	for _, item := range dirs {
		entries, err := os.ReadDir(item.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, fmt.Errorf("inbox: list %s notices: %w", item.status, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			msg, err := readMessage(filepath.Join(item.path, entry.Name()), target, entry.Name())
			if err != nil {
				return nil, 0, err
			}
			notices = append(notices, Notice{Message: msg, Status: item.status})
		}
	}
	sort.Slice(notices, func(i, j int) bool {
		if notices[i].Message.Created.Equal(notices[j].Message.Created) {
			return notices[i].Message.ID < notices[j].Message.ID
		}
		return notices[i].Message.Created.Before(notices[j].Message.Created)
	})
	total := len(notices)
	if offset >= total {
		return nil, total, nil
	}
	end := min(total, offset+limit)
	return notices[offset:end], total, nil
}

// Read finds one notice by id across pending and archived storage.
func (s Store) Read(target, id string) (Notice, error) {
	if !validMessageID(id) {
		return Notice{}, errors.New("inbox: invalid message id")
	}
	dirs, err := s.noticeDirs(target, StatusAll)
	if err != nil {
		return Notice{}, err
	}
	name := id + ".json"
	for _, item := range dirs {
		path := filepath.Join(item.path, name)
		msg, err := readMessage(path, target, name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Notice{}, err
		}
		return Notice{Message: msg, Status: item.status}, nil
	}
	return Notice{}, os.ErrNotExist
}

// ReadOffset returns the next byte offset that must be read for a pending
// notice. Read progress is contiguous: callers cannot skip unseen content.
func (s Store) ReadOffset(target, id string) (int, error) {
	progress, found, err := s.readProgress(target, id)
	if err != nil || !found {
		return 0, err
	}
	return progress.Offset, nil
}

// RecordRead advances durable contiguous read progress after a notice chunk
// has been written successfully to the caller.
func (s Store) RecordRead(target, id string, from, to, bodyBytes int) error {
	if from < 0 || to < from || to > bodyBytes {
		return errors.New("inbox: invalid notice read range")
	}
	notice, err := s.Read(target, id)
	if err != nil {
		return err
	}
	if notice.Status != StatusNew && notice.Status != StatusProcessing {
		return errors.New("inbox: read progress is recorded only for pending notices")
	}
	if len(notice.Message.Body) != bodyBytes {
		return errors.New("inbox: notice body size changed")
	}
	progress, found, err := s.readProgress(target, id)
	if err != nil {
		return err
	}
	expected := 0
	if found {
		if progress.BodyBytes != bodyBytes {
			return errors.New("inbox: notice read progress body size mismatch")
		}
		expected = progress.Offset
	}
	if from > expected {
		return fmt.Errorf("inbox: notice read must continue at offset %d", expected)
	}
	if to <= expected {
		return nil
	}
	progress = ReadProgress{ID: id, Offset: to, BodyBytes: bodyBytes, Updated: time.Now().UTC()}
	return s.writeReadProgress(target, progress)
}

// FullyRead reports whether the complete body of this pending notice has been
// exposed through sequential inbox-read calls.
func (s Store) FullyRead(delivery Delivery) (bool, error) {
	if delivery.path == "" {
		return false, errors.New("inbox: delivery has no claim path")
	}
	progress, found, err := s.readProgress(delivery.Message.To, delivery.Message.ID)
	if err != nil || !found {
		return false, err
	}
	return progress.BodyBytes == len(delivery.Message.Body) && progress.Offset == progress.BodyBytes, nil
}

// ArchiveRead archives a batch only after every named notice has been fully
// read. Main notices normally remain new until this explicit acknowledgement;
// processing remains accepted for workflow-era recovery and diagnostics.
func (s Store) ArchiveRead(target string, ids []string) error {
	if len(ids) == 0 {
		return errors.New("inbox: at least one message id is required")
	}
	seen := make(map[string]struct{}, len(ids))
	deliveries := make([]Delivery, 0, len(ids))
	for _, id := range ids {
		if !validMessageID(id) {
			return fmt.Errorf("inbox: invalid message id %q", id)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("inbox: duplicate message id %q", id)
		}
		seen[id] = struct{}{}
		notice, err := s.Read(target, id)
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inbox: notice %s is not pending", id)
		}
		if err != nil {
			return err
		}
		if notice.Status != StatusNew && notice.Status != StatusProcessing {
			return fmt.Errorf("inbox: notice %s is not pending", id)
		}
		newDir, processingDir, err := s.targetDirs(target)
		if err != nil {
			return err
		}
		dir := newDir
		if notice.Status == StatusProcessing {
			dir = processingDir
		}
		path := filepath.Join(dir, id+".json")
		msg := notice.Message
		delivery := Delivery{Message: msg, path: path}
		fullyRead, err := s.FullyRead(delivery)
		if err != nil {
			return err
		}
		if !fullyRead {
			return fmt.Errorf("inbox: notice %s has not been fully read", id)
		}
		deliveries = append(deliveries, delivery)
	}
	for _, delivery := range deliveries {
		if err := s.Archive(delivery); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) readProgress(target, id string) (ReadProgress, bool, error) {
	path, err := s.readProgressPath(target, id)
	if err != nil {
		return ReadProgress{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ReadProgress{}, false, nil
	}
	if err != nil {
		return ReadProgress{}, false, fmt.Errorf("inbox: read notice progress: %w", err)
	}
	var progress ReadProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return ReadProgress{}, false, fmt.Errorf("inbox: decode notice progress: %w", err)
	}
	if progress.ID != id || progress.Offset < 0 || progress.Offset > progress.BodyBytes {
		return ReadProgress{}, false, errors.New("inbox: invalid notice read progress")
	}
	return progress, true, nil
}

func (s Store) writeReadProgress(target string, progress ReadProgress) error {
	path, err := s.readProgressPath(target, progress.ID)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("inbox: create notice progress directory: %w", err)
	}
	data, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("inbox: encode notice progress: %w", err)
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(dir, "."+progress.ID+".*.tmp")
	if err != nil {
		return fmt.Errorf("inbox: create notice progress: %w", err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return fmt.Errorf("inbox: protect notice progress: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("inbox: write notice progress: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("inbox: sync notice progress: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("inbox: close notice progress: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("inbox: publish notice progress: %w", err)
	}
	ok = true
	return syncDir(dir)
}

func (s Store) clearReadProgress(target, id string) error {
	path, err := s.readProgressPath(target, id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inbox: clear notice progress: %w", err)
	}
	return syncDir(filepath.Dir(path))
}

func (s Store) readProgressPath(target, id string) (string, error) {
	if !validMessageID(id) {
		return "", errors.New("inbox: invalid message id")
	}
	newDir, _, err := s.targetDirs(target)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(newDir), "reads", id+".json"), nil
}

type noticeDir struct {
	status NoticeStatus
	path   string
}

func (s Store) noticeDirs(target string, status NoticeStatus) ([]noticeDir, error) {
	newDir, processingDir, err := s.targetDirs(target)
	if err != nil {
		return nil, err
	}
	archiveDir := filepath.Join(filepath.Dir(newDir), string(StatusArchived))
	switch status {
	case StatusNew:
		return []noticeDir{{StatusNew, newDir}}, nil
	case StatusProcessing:
		return []noticeDir{{StatusProcessing, processingDir}}, nil
	case StatusArchived:
		return []noticeDir{{StatusArchived, archiveDir}}, nil
	case StatusPending:
		return []noticeDir{{StatusNew, newDir}, {StatusProcessing, processingDir}}, nil
	case StatusAll:
		return []noticeDir{{StatusNew, newDir}, {StatusProcessing, processingDir}, {StatusArchived, archiveDir}}, nil
	default:
		return nil, fmt.Errorf("inbox: unknown notice status %q", status)
	}
}

func readMessage(path, target, filename string) (Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Message{}, err
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return Message{}, fmt.Errorf("inbox: decode message: %w", err)
	}
	if msg.To != target || msg.ID+".json" != filename {
		return Message{}, errors.New("inbox: message identity mismatch")
	}
	return msg, nil
}

func validMessageID(id string) bool {
	if len(id) != len("msg_")+32 || !strings.HasPrefix(id, "msg_") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(id, "msg_"))
	return err == nil
}

func (s Store) targetDirs(target string) (string, string, error) {
	if strings.TrimSpace(s.Root) == "" {
		return "", "", errors.New("inbox: state root is required")
	}
	if err := validateTarget(target); err != nil {
		return "", "", err
	}
	root := filepath.Join(s.Root, "inbox", encodeTarget(target))
	return filepath.Join(root, "new"), filepath.Join(root, "processing"), nil
}

func validateTarget(target string) error {
	switch {
	case strings.TrimSpace(target) == "":
		return errors.New("inbox: destination is required")
	case len(target) > 256:
		return errors.New("inbox: destination is too long")
	case strings.ContainsAny(target, "\r\n\x00"):
		return errors.New("inbox: destination contains an invalid character")
	}
	return nil
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func encodeTarget(target string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(target))
}

func messageID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "msg_" + hex.EncodeToString(raw[:]), nil
}

func wakeSocket(path, target string) error {
	conn, err := net.DialTimeout("unixgram", path, 100*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	_, err = conn.Write([]byte(target))
	return err
}
