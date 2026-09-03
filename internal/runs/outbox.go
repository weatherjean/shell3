package runs

// The outbox holds opaque completion and running-job records until the runtime
// successfully delivers or resolves them. Entries survive process restarts.

// OutboxRow is one persisted outbox entry.
type OutboxRow struct {
	ID   int64
	Kind string
	JSON string
}

// OutboxPut appends one row and returns its id.
func (s *Store) OutboxPut(kind, json string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO outbox (kind, json) VALUES (?,?)`, kind, json)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// OutboxDelete removes one row; deleting an absent id is not an error.
func (s *Store) OutboxDelete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM outbox WHERE id = ?`, id)
	return err
}

// OutboxLoadAll returns every row in insertion order.
func (s *Store) OutboxLoadAll() ([]OutboxRow, error) {
	rows, err := s.db.Query(`SELECT id, kind, json FROM outbox ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OutboxRow
	for rows.Next() {
		var r OutboxRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.JSON); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
