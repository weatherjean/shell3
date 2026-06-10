// Package store provides a SQLite-backed store for history and sessions.
package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ChunkSize is the number of history turns per chunk returned by HistoryGet.
const ChunkSize = 25

// Store wraps a SQLite database with tables for sessions and history.
type Store struct{ db *sql.DB }

// Open opens or creates the SQLite store at path and runs schema migrations.
//
// SQLite permits only one writer at a time. SetMaxOpenConns(1) serializes
// in-process writers; the busy_timeout pragma is a backstop for cross-process
// contention. We do NOT set WAL: it is unneeded here and unsafe for :memory:.
func Open(path string) (*Store, error) {
	dsn := path
	// Append the busy_timeout pragma via modernc's query-param DSN syntax; the
	// ?/& branch handles a path that already carries DSN query params.
	if strings.Contains(path, "?") {
		dsn += "&_pragma=busy_timeout(5000)"
	} else {
		dsn += "?_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at TEXT NOT NULL,
			ended_at   TEXT,
			summary    TEXT
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS history USING fts5(
			content,
			session_id UNINDEXED,
			role       UNINDEXED,
			created_at UNINDEXED
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("store: migrate: %w", err)
		}
	}
	return nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

// parseRFC3339 parses a store-written RFC3339 timestamp, returning the zero
// time.Time on error. Malformed stored timestamps deliberately fall back to
// zero (the write format is store-controlled, so this only fires on corruption).
func parseRFC3339(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// StartSession inserts a new session row and returns its id.
func (s *Store) StartSession() (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`INSERT INTO sessions(started_at) VALUES(?)`, now)
	if err != nil {
		return 0, fmt.Errorf("store: start session: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: start session: last insert id: %w", err)
	}
	return id, nil
}

// EndSession sets ended_at for the given session.
func (s *Store) EndSession(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE sessions SET ended_at = ? WHERE id = ?`, now, id); err != nil {
		return fmt.Errorf("store: end session %d: %w", id, err)
	}
	return nil
}

// SessionMeta summarizes one stored conversation for listing.
type SessionMeta struct {
	ID        int64
	StartedAt time.Time
	EndedAt   time.Time
	Summary   string
	NumMsgs   int
	Preview   string // first user message, truncated
}

// ListSessions returns up to limit most-recent sessions (newest first), each
// with a message count and a preview of its first user message.
func (s *Store) ListSessions(limit int) ([]SessionMeta, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT s.id, s.started_at, COALESCE(s.ended_at,''), COALESCE(s.summary,''),
		       (SELECT COUNT(*) FROM history h WHERE h.session_id = s.id),
		       COALESCE((SELECT h.content FROM history h
		                 WHERE h.session_id = s.id AND h.role='user'
		                 ORDER BY h.rowid ASC LIMIT 1), '')
		FROM sessions s
		ORDER BY s.id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	defer rows.Close()
	var out []SessionMeta
	for rows.Next() {
		var m SessionMeta
		var started, ended, preview string
		if err := rows.Scan(&m.ID, &started, &ended, &m.Summary, &m.NumMsgs, &preview); err != nil {
			return nil, fmt.Errorf("store: list sessions: scan: %w", err)
		}
		m.StartedAt = parseRFC3339(started)
		if ended != "" {
			m.EndedAt = parseRFC3339(ended)
		}
		m.Preview = truncateRunes(preview, 120)
		out = append(out, m)
	}
	return out, rows.Err()
}

// SessionTurns returns every stored turn for one session, in order.
func (s *Store) SessionTurns(sessionID int64) ([]HistoryTurn, error) {
	rows, err := s.db.Query(
		`SELECT session_id, role, content, created_at FROM history
		 WHERE session_id = ? ORDER BY rowid ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: session turns %d: %w", sessionID, err)
	}
	defer rows.Close()
	var out []HistoryTurn
	for rows.Next() {
		var t HistoryTurn
		var created string
		if err := rows.Scan(&t.SessionID, &t.Role, &t.Content, &created); err != nil {
			return nil, fmt.Errorf("store: session turns: scan: %w", err)
		}
		t.CreatedAt = parseRFC3339(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

// truncateRunes shortens s to at most n runes, appending an ellipsis if cut.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// AppendHistory stores one conversation turn in the history FTS5 table.
func (s *Store) AppendHistory(sessionID int64, role, content string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO history(content, session_id, role, created_at) VALUES(?, ?, ?, ?)`,
		content, sessionID, role, now,
	)
	if err != nil {
		return fmt.Errorf("store: append history: %w", err)
	}
	return nil
}

// HistoryTurn is one stored conversation turn.
type HistoryTurn struct {
	SessionID int64
	Role      string
	Content   string
	CreatedAt time.Time
	// Chunk is the chunk index this turn belongs to within its session.
	// Populated for search results so the model can fetch surrounding context.
	Chunk int
}

// HistoryGetResult is returned when fetching a chunk of one session.
type HistoryGetResult struct {
	SessionID        int64
	Chunk            int
	TotalChunks      int
	PrevSessionID    int64 // 0 if none
	NextSessionID    int64 // 0 if none
	SessionStartedAt time.Time
	SessionEndedAt   time.Time // zero if session still in progress
	Turns            []HistoryTurn
}

// HistorySearchResult is returned for FTS history queries.
type HistorySearchResult struct {
	TotalHits int
	Hits      []HistoryTurn
}

// HistoryGet returns a chunk of one session.
//
//   - sessionID == 0 → use the latest completed session (most recent
//     row in sessions where ended_at IS NOT NULL).
//   - chunk indexes oldest→newest within the session, ChunkSize turns each.
//   - PrevSessionID/NextSessionID walk completed sessions by id; NextSessionID
//     may point to the current in-progress session.
func (s *Store) HistoryGet(sessionID int64, chunk int) (HistoryGetResult, error) {
	if chunk < 0 {
		return HistoryGetResult{}, fmt.Errorf("store: history get: chunk must be >= 0")
	}

	if sessionID == 0 {
		err := s.db.QueryRow(`
			SELECT id FROM sessions
			WHERE ended_at IS NOT NULL
			ORDER BY id DESC LIMIT 1
		`).Scan(&sessionID)
		if err == sql.ErrNoRows {
			return HistoryGetResult{}, nil
		}
		if err != nil {
			return HistoryGetResult{}, fmt.Errorf("store: history get: latest session: %w", err)
		}
	}

	var startedAt string
	var endedAt sql.NullString
	err := s.db.QueryRow(`SELECT started_at, ended_at FROM sessions WHERE id = ?`, sessionID).
		Scan(&startedAt, &endedAt)
	if err == sql.ErrNoRows {
		return HistoryGetResult{}, fmt.Errorf("store: history get: session %d not found", sessionID)
	}
	if err != nil {
		return HistoryGetResult{}, fmt.Errorf("store: history get: session row: %w", err)
	}

	var totalTurns int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM history WHERE CAST(session_id AS INTEGER) = ?`, sessionID,
	).Scan(&totalTurns); err != nil {
		return HistoryGetResult{}, fmt.Errorf("store: history get: count: %w", err)
	}

	totalChunks := (totalTurns + ChunkSize - 1) / ChunkSize
	if totalChunks == 0 {
		totalChunks = 1
	}

	rows, err := s.db.Query(`
		SELECT role, content, created_at FROM history
		WHERE CAST(session_id AS INTEGER) = ?
		ORDER BY rowid ASC
		LIMIT ? OFFSET ?
	`, sessionID, ChunkSize, chunk*ChunkSize)
	if err != nil {
		return HistoryGetResult{}, fmt.Errorf("store: history get: turns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var turns []HistoryTurn
	for rows.Next() {
		var t HistoryTurn
		var createdAt string
		if err := rows.Scan(&t.Role, &t.Content, &createdAt); err != nil {
			return HistoryGetResult{}, fmt.Errorf("store: history get: scan: %w", err)
		}
		t.SessionID = sessionID
		t.Chunk = chunk
		t.CreatedAt = parseRFC3339(createdAt)
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return HistoryGetResult{}, err
	}

	var prevID, nextID int64
	_ = s.db.QueryRow(`
		SELECT id FROM sessions
		WHERE ended_at IS NOT NULL AND id < ?
		ORDER BY id DESC LIMIT 1
	`, sessionID).Scan(&prevID)
	_ = s.db.QueryRow(`
		SELECT id FROM sessions
		WHERE id > ?
		ORDER BY id ASC LIMIT 1
	`, sessionID).Scan(&nextID)

	res := HistoryGetResult{
		SessionID:     sessionID,
		Chunk:         chunk,
		TotalChunks:   totalChunks,
		PrevSessionID: prevID,
		NextSessionID: nextID,
		Turns:         turns,
	}
	res.SessionStartedAt = parseRFC3339(startedAt)
	if endedAt.Valid {
		res.SessionEndedAt = parseRFC3339(endedAt.String)
	}
	return res, nil
}

// HistorySearchExpr runs an FTS5 search over history content using a
// pre-built MATCH expression (typically from BuildFTSExpr). Empty expr
// short-circuits to an empty result.
func (s *Store) HistorySearchExpr(expr string, limit int) (HistorySearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	if expr == "" {
		return HistorySearchResult{}, nil
	}

	// The correlated subquery computes each hit's chunk index in the same
	// round-trip: count the turns earlier than this row (by rowid) within the
	// same session, then divide by ChunkSize. Folding it in avoids an N+1
	// per-result COUNT query. The count must range over the full history table
	// (not just matched rows), so it can't be a window over the result set.
	rows, err := s.db.Query(`
		SELECT rowid, CAST(session_id AS INTEGER), role, content, created_at,
			(SELECT COUNT(*) FROM history e
			 WHERE CAST(e.session_id AS INTEGER) = CAST(history.session_id AS INTEGER)
			   AND e.rowid < history.rowid) AS earlier
		FROM history
		WHERE history MATCH ?
		ORDER BY rank
		LIMIT ?
	`, expr, limit)
	if err != nil {
		return HistorySearchResult{}, fmt.Errorf("store: history search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hits []HistoryTurn
	for rows.Next() {
		var rowid, earlier int64
		var t HistoryTurn
		var createdAt string
		if err := rows.Scan(&rowid, &t.SessionID, &t.Role, &t.Content, &createdAt, &earlier); err != nil {
			return HistorySearchResult{}, fmt.Errorf("store: history search: scan: %w", err)
		}
		t.CreatedAt = parseRFC3339(createdAt)
		t.Chunk = int(earlier) / ChunkSize
		hits = append(hits, t)
	}
	if err := rows.Err(); err != nil {
		return HistorySearchResult{}, err
	}

	return HistorySearchResult{TotalHits: len(hits), Hits: hits}, nil
}
