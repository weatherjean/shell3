package runs

// Each front-end surface's ONE long-lived conversation records its session id
// in the same database as the sessions it points at. surface namespaces the
// front-ends ("telegram", "serve") so two transports never cross-resolve.

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
