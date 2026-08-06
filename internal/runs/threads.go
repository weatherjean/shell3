package runs

// The front-end thread indexes (each front-end's own message/thread id →
// session id) live in the same database as the sessions they point at.
// surface namespaces the front-ends (e.g. "web") so their ids never
// cross-resolve.

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

// ThreadMeta is one thread's full record, for surfaces that carry
// a title/preview/timestamps/tombstone on top of the plain msgID→sessionID
// mapping ThreadRecord/ThreadLookup give the simpler surfaces.
type ThreadMeta struct {
	ID        string
	SessionID string
	Title     string
	Preview   string
	Created   string
	Updated   string
	Deleted   bool
}

// ThreadUpsertMeta writes a thread's full record for surface, replacing
// whatever was there. Last write wins, same as ThreadRecord.
func (s *Store) ThreadUpsertMeta(surface string, m ThreadMeta) error {
	_, err := s.db.Exec(`INSERT INTO threads
		(surface, msg_id, session_id, title, preview, created_at, updated_at, deleted)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT (surface, msg_id) DO UPDATE SET
			session_id=excluded.session_id, title=excluded.title, preview=excluded.preview,
			created_at=excluded.created_at, updated_at=excluded.updated_at, deleted=excluded.deleted`,
		surface, m.ID, m.SessionID, m.Title, m.Preview, m.Created, m.Updated, boolToInt(m.Deleted))
	return err
}

// ThreadListMeta returns every thread recorded for surface, in no particular
// order — the caller sorts.
func (s *Store) ThreadListMeta(surface string) ([]ThreadMeta, error) {
	rows, err := s.db.Query(`SELECT msg_id, session_id, title, preview, created_at, updated_at, deleted
		FROM threads WHERE surface=?`, surface)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ThreadMeta
	for rows.Next() {
		var m ThreadMeta
		var deleted int
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Title, &m.Preview, &m.Created, &m.Updated, &deleted); err != nil {
			return nil, err
		}
		m.Deleted = deleted != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
