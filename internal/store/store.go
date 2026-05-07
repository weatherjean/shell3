// Package store provides a SQLite-backed store for memories, history, and sessions.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ChunkSize is the number of history turns per chunk returned by HistoryGet.
const ChunkSize = 25

// Store wraps a SQLite database with tables for sessions, history, and memories.
type Store struct{ db *sql.DB }

// Open opens or creates the SQLite store at path and runs schema migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
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
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories USING fts5(
			key,
			value,
			core       UNINDEXED,
			updated_at UNINDEXED
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("store: migrate: %w", err)
		}
	}
	return migrateMemoriesAddCore(db)
}

// migrateMemoriesAddCore adds the `core` column to legacy memories tables
// that predate the column. FTS5 has no ALTER ADD COLUMN, so we copy-rebuild.
func migrateMemoriesAddCore(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(memories)`)
	if err != nil {
		return fmt.Errorf("store: inspect memories: %w", err)
	}
	hasCore := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: scan table_info: %w", err)
		}
		if name == "core" {
			hasCore = true
		}
	}
	_ = rows.Close()
	if hasCore {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("store: migrate memories: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	migration := []string{
		`CREATE VIRTUAL TABLE memories_new USING fts5(
			key,
			value,
			core       UNINDEXED,
			updated_at UNINDEXED
		)`,
		`INSERT INTO memories_new(rowid, key, value, core, updated_at)
			SELECT rowid, key, value, 0, updated_at FROM memories`,
		`DROP TABLE memories`,
		`ALTER TABLE memories_new RENAME TO memories`,
	}
	for _, s := range migration {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("store: migrate memories: %w", err)
		}
	}
	return tx.Commit()
}

// Close closes the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

// MemoryEntry is one memory record.
type MemoryEntry struct {
	Key       string
	Value     string
	Core      bool
	UpdatedAt time.Time
}

// MemoryUpsert inserts or updates a memory entry.
//
//   - Empty value deletes any existing entry for key.
//   - On insert, core defaults to false unless explicitly set.
//   - On update, core is preserved unless explicitly set (pass *bool).
func (s *Store) MemoryUpsert(key, value string, core *bool) error {
	if key == "" {
		return fmt.Errorf("store: memory upsert: empty key")
	}
	if value == "" {
		if _, err := s.db.Exec(`DELETE FROM memories WHERE key = ?`, key); err != nil {
			return fmt.Errorf("store: memory delete %q: %w", key, err)
		}
		return nil
	}

	var existingCore int
	var existed bool
	err := s.db.QueryRow(`SELECT core FROM memories WHERE key = ?`, key).Scan(&existingCore)
	switch {
	case err == sql.ErrNoRows:
		existed = false
	case err != nil:
		return fmt.Errorf("store: memory probe %q: %w", key, err)
	default:
		existed = true
	}

	finalCore := 0
	switch {
	case core != nil && *core:
		finalCore = 1
	case core != nil && !*core:
		finalCore = 0
	case existed:
		finalCore = existingCore
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`DELETE FROM memories WHERE key = ?`, key); err != nil {
		return fmt.Errorf("store: memory delete: %w", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO memories(key, value, core, updated_at) VALUES(?, ?, ?, ?)`,
		key, value, finalCore, now,
	); err != nil {
		return fmt.Errorf("store: memory insert: %w", err)
	}
	return nil
}

// MemoryQuery lists memory entries newest-first. coreOnly filters to core
// entries. limit caps results; pass <=0 for default 50. For full-text
// search use MemorySearchExpr.
func (s *Store) MemoryQuery(coreOnly bool, limit int) ([]MemoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	var (
		rows *sql.Rows
		err  error
	)
	if coreOnly {
		rows, err = s.db.Query(`
			SELECT key, value, core, updated_at FROM memories
			WHERE core = 1
			ORDER BY updated_at DESC, rowid DESC
			LIMIT ?
		`, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT key, value, core, updated_at FROM memories
			ORDER BY updated_at DESC, rowid DESC
			LIMIT ?
		`, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("store: memory query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanMemoryRows(rows)
}

// MemorySearchExpr runs an FTS5 search over memories using a pre-built
// MATCH expression (typically from BuildFTSExpr). Empty expr short-circuits
// to a clean empty result.
func (s *Store) MemorySearchExpr(expr string, coreOnly bool, limit int) ([]MemoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if expr == "" {
		return nil, nil
	}
	q := `SELECT key, value, core, updated_at FROM memories WHERE memories MATCH ?`
	if coreOnly {
		q += ` AND core = 1`
	}
	q += ` ORDER BY rank LIMIT ?`
	rows, err := s.db.Query(q, expr, limit)
	if err != nil {
		return nil, fmt.Errorf("store: memory search: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanMemoryRows(rows)
}

func scanMemoryRows(rows *sql.Rows) ([]MemoryEntry, error) {
	var results []MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		var coreInt int
		var updatedAt string
		if err := rows.Scan(&e.Key, &e.Value, &coreInt, &updatedAt); err != nil {
			return nil, fmt.Errorf("store: memory scan: %w", err)
		}
		e.Core = coreInt != 0
		e.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		results = append(results, e)
	}
	return results, rows.Err()
}

// StartSession inserts a new session row and returns its id.
func (s *Store) StartSession() (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`INSERT INTO sessions(started_at) VALUES(?)`, now)
	if err != nil {
		return 0, fmt.Errorf("store: start session: %w", err)
	}
	return res.LastInsertId()
}

// EndSession sets ended_at for the given session.
func (s *Store) EndSession(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE sessions SET ended_at = ? WHERE id = ?`, now, id); err != nil {
		return fmt.Errorf("store: end session %d: %w", id, err)
	}
	return nil
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
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
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
	res.SessionStartedAt, _ = time.Parse(time.RFC3339, startedAt)
	if endedAt.Valid {
		res.SessionEndedAt, _ = time.Parse(time.RFC3339, endedAt.String)
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

	rows, err := s.db.Query(`
		SELECT rowid, CAST(session_id AS INTEGER), role, content, created_at
		FROM history
		WHERE history MATCH ?
		ORDER BY rank
		LIMIT ?
	`, expr, limit)
	if err != nil {
		return HistorySearchResult{}, fmt.Errorf("store: history search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type hitWithRowid struct {
		rowid int64
		turn  HistoryTurn
	}
	var raw []hitWithRowid
	for rows.Next() {
		var rowid int64
		var t HistoryTurn
		var createdAt string
		if err := rows.Scan(&rowid, &t.SessionID, &t.Role, &t.Content, &createdAt); err != nil {
			return HistorySearchResult{}, fmt.Errorf("store: history search: scan: %w", err)
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		raw = append(raw, hitWithRowid{rowid, t})
	}
	if err := rows.Err(); err != nil {
		return HistorySearchResult{}, err
	}

	// Compute chunk index for each hit: count earlier turns in the same session by rowid.
	hits := make([]HistoryTurn, 0, len(raw))
	for _, r := range raw {
		var earlier int
		err := s.db.QueryRow(`
			SELECT COUNT(*) FROM history
			WHERE CAST(session_id AS INTEGER) = ? AND rowid < ?
		`, r.turn.SessionID, r.rowid).Scan(&earlier)
		if err != nil {
			return HistorySearchResult{}, fmt.Errorf("store: history search: chunk index: %w", err)
		}
		r.turn.Chunk = earlier / ChunkSize
		hits = append(hits, r.turn)
	}

	return HistorySearchResult{TotalHits: len(hits), Hits: hits}, nil
}
