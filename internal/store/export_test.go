package store

// MaxOpenConns exposes the configured pool limit for tests.
func (s *Store) MaxOpenConns() int { return s.db.Stats().MaxOpenConnections }
