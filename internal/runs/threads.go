package runs

// Each front-end surface's ONE long-lived conversation records its session id
// in the same database as the sessions it points at. surface namespaces the
// surface by transport ("telegram" is the only one today) so a future
// front-end could never cross-resolve another's conversation.

// SetCurrentSession records id as surface's current conversation session.
// Last write wins.
func (s *Store) SetCurrentSession(surface, id string) error {
	_, err := s.db.Exec(`INSERT INTO threads (surface, session_id)
		VALUES (?,?)
		ON CONFLICT (surface) DO UPDATE SET session_id=excluded.session_id`,
		surface, id)
	return err
}

// CurrentSession returns the session id recorded for surface, if any.
func (s *Store) CurrentSession(surface string) (string, bool) {
	var id string
	err := s.db.QueryRow(`SELECT session_id FROM threads WHERE surface=?`, surface).Scan(&id)
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
	err := s.db.QueryRow(`SELECT surface FROM threads WHERE session_id=? LIMIT 1`, sessionID).Scan(&surface)
	if err != nil {
		return "", false
	}
	return surface, true
}

// SurfacesWithPrefix lists surface → session id for every surface starting
// with prefix — the enrolled-rooms listing ("telegram:"). An unreadable row
// is skipped rather than failing the listing: this feeds a view, never a
// decision.
func (s *Store) SurfacesWithPrefix(prefix string) map[string]string {
	out := map[string]string{}
	rows, err := s.db.Query(`SELECT surface, session_id FROM threads WHERE surface LIKE ? || '%'`, prefix)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var surface, id string
		if err := rows.Scan(&surface, &id); err != nil {
			continue
		}
		out[surface] = id
	}
	return out
}
