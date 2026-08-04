package runs

// The front-end thread indexes (Telegram message id → session id, and the
// serve transport's equivalent) live in the same database as the sessions
// they point at. surface namespaces the two front-ends ("telegram", "serve")
// so their ids never cross-resolve.

// ThreadRecord maps msgID to sessionID for the given surface. Last write
// wins; a failed write is reported but the caller may treat it as
// best-effort (a lost index line loses one thread resume, never the
// conversation).
func (s *Store) ThreadRecord(surface, msgID, sessionID string) error {
	_, err := s.db.Exec(`INSERT INTO threads (surface, msg_id, session_id)
		VALUES (?,?,?)
		ON CONFLICT (surface, msg_id) DO UPDATE SET session_id=excluded.session_id`,
		surface, msgID, sessionID)
	return err
}

// ThreadLookup returns the session id recorded for msgID on surface, if any.
func (s *Store) ThreadLookup(surface, msgID string) (string, bool) {
	var id string
	err := s.db.QueryRow(`SELECT session_id FROM threads
		WHERE surface=? AND msg_id=?`, surface, msgID).Scan(&id)
	if err != nil {
		return "", false
	}
	return id, true
}
