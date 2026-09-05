package runs

// Each front-end surface's ONE long-lived conversation records its session id
// in the same database as the sessions it points at. surface namespaces the
// surface by transport ("telegram" is the only one today) so a future
// front-end could never cross-resolve another's conversation.

// SetCurrentSession records id as surface's current conversation session.
// Last write wins.
func (s *Store) SetCurrentSession(surface, id string) error {
	_, err := s.db.Exec(`INSERT INTO current_sessions (surface, session_id)
		VALUES (?,?)
		ON CONFLICT (surface) DO UPDATE SET session_id=excluded.session_id`,
		surface, id)
	return err
}

// CurrentSession returns the session id recorded for surface, if any.
func (s *Store) CurrentSession(surface string) (string, bool) {
	var id string
	err := s.db.QueryRow(`SELECT session_id FROM current_sessions WHERE surface=?`, surface).Scan(&id)
	if err != nil {
		return "", false
	}
	return id, true
}

// SurfaceForSession is CurrentSession backwards: which surface, if any, has
// this session as its current conversation. The Telegram front-end uses it to
// find the ROOM a completion belongs to after a restart, before any room has
// been used again — the live registry is empty then, and without this the
// answer would degrade to "the home chat" for every recovered job.
//
// A session can only be one surface's current conversation (surface is the
// primary key and a session id is written to one row), so the first match is
// the answer.
func (s *Store) SurfaceForSession(sessionID string) (string, bool) {
	var surface string
	err := s.db.QueryRow(`SELECT surface FROM current_sessions WHERE session_id=? LIMIT 1`, sessionID).Scan(&surface)
	if err != nil {
		return "", false
	}
	return surface, true
}
