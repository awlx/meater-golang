// Package store persists cooking sessions ("cooks") and their temperature
// samples in SQLite so the current cook and recent history survive a service
// restart. It uses the pure-Go modernc.org/sqlite driver so the binary still
// builds statically with CGO disabled.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// keepEndedCooks is how many finished cooks to retain (older ones are pruned).
const keepEndedCooks = 10

// Store is a SQLite-backed persistence layer for cooks and samples.
type Store struct {
	db *sql.DB
}

// CookMeta describes a cooking session.
type CookMeta struct {
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	MeatType          string     `json:"meatType"`
	StartedAt         time.Time  `json:"startedAt"`
	EndedAt           *time.Time `json:"endedAt"`
	TargetCelsius     float64    `json:"targetCelsius"`
	MaxTipCelsius     float64    `json:"maxTipCelsius"`
	MaxAmbientCelsius float64    `json:"maxAmbientCelsius"`
	Samples           int        `json:"samples"`
	Active            bool       `json:"active"`
}

// Point is a single timestamped temperature sample.
type Point struct {
	At             time.Time `json:"at"`
	TipCelsius     float64   `json:"tipCelsius"`
	AmbientCelsius float64   `json:"ambientCelsius"`
}

// Open opens (creating if needed) the SQLite database at path and runs
// migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	// SQLite handles one writer at a time; keep a single connection to avoid
	// "database is locked" under our low write rate.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS cooks (
	id                  INTEGER PRIMARY KEY AUTOINCREMENT,
	name                TEXT    NOT NULL DEFAULT '',
	target_celsius      REAL    NOT NULL DEFAULT 0,
	started_at          INTEGER NOT NULL,
	ended_at            INTEGER,
	max_tip_celsius     REAL    NOT NULL DEFAULT 0,
	max_ambient_celsius REAL    NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS samples (
	cook_id        INTEGER NOT NULL,
	at             INTEGER NOT NULL,
	tip_celsius    REAL    NOT NULL,
	ambient_celsius REAL   NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_samples_cook ON samples(cook_id, at);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// meat_type was added later; add it if an older database predates it.
	if err := s.addColumnIfMissing("cooks", "meat_type", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate meat_type: %w", err)
	}
	return nil
}

// addColumnIfMissing adds a column to a table when it is not already present,
// so repeated startups on an existing database are safe (SQLite has no
// ADD COLUMN IF NOT EXISTS).
func (s *Store) addColumnIfMissing(table, column, decl string) error {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, decl))
	return err
}

// StartCook inserts a new cook and returns its id.
func (s *Store) StartCook(name, meatType string, targetCelsius float64, at time.Time) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO cooks(name, meat_type, target_celsius, started_at) VALUES(?, ?, ?, ?)`,
		name, meatType, targetCelsius, at.UnixMilli(),
	)
	if err != nil {
		return 0, fmt.Errorf("start cook: %w", err)
	}
	return res.LastInsertId()
}

// AppendSample records a sample for a cook and updates the running maxima.
func (s *Store) AppendSample(cookID int64, at time.Time, tip, ambient float64) error {
	if _, err := s.db.Exec(
		`INSERT INTO samples(cook_id, at, tip_celsius, ambient_celsius) VALUES(?, ?, ?, ?)`,
		cookID, at.UnixMilli(), tip, ambient,
	); err != nil {
		return fmt.Errorf("append sample: %w", err)
	}
	_, err := s.db.Exec(
		`UPDATE cooks SET max_tip_celsius = MAX(max_tip_celsius, ?),
		                  max_ambient_celsius = MAX(max_ambient_celsius, ?)
		 WHERE id = ?`,
		tip, ambient, cookID,
	)
	return err
}

// EndCook marks a cook finished at the given time (no-op if already ended).
func (s *Store) EndCook(cookID int64, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE cooks SET ended_at = ? WHERE id = ? AND ended_at IS NULL`,
		at.UnixMilli(), cookID,
	)
	return err
}

// RenameCook sets a cook's name.
func (s *Store) RenameCook(cookID int64, name string) error {
	_, err := s.db.Exec(`UPDATE cooks SET name = ? WHERE id = ?`, name, cookID)
	return err
}

// SetCookMeatType sets a cook's meat type label.
func (s *Store) SetCookMeatType(cookID int64, meatType string) error {
	_, err := s.db.Exec(`UPDATE cooks SET meat_type = ? WHERE id = ?`, meatType, cookID)
	return err
}

// SetCookTarget updates a cook's target temperature.
func (s *Store) SetCookTarget(cookID int64, targetCelsius float64) error {
	_, err := s.db.Exec(`UPDATE cooks SET target_celsius = ? WHERE id = ?`, targetCelsius, cookID)
	return err
}

// CurrentOpenCook returns the most recent cook with no end time, or nil.
func (s *Store) CurrentOpenCook() (*CookMeta, error) {
	row := s.db.QueryRow(
		`SELECT id, name, meat_type, target_celsius, started_at, ended_at, max_tip_celsius, max_ambient_celsius
		 FROM cooks WHERE ended_at IS NULL ORDER BY started_at DESC LIMIT 1`,
	)
	m, err := scanCook(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// LastSampleAt returns the timestamp of the most recent sample for a cook.
// ok is false when the cook has no samples.
func (s *Store) LastSampleAt(cookID int64) (at time.Time, ok bool, err error) {
	var ms sql.NullInt64
	err = s.db.QueryRow(`SELECT MAX(at) FROM samples WHERE cook_id = ?`, cookID).Scan(&ms)
	if err != nil {
		return time.Time{}, false, err
	}
	if !ms.Valid {
		return time.Time{}, false, nil
	}
	return time.UnixMilli(ms.Int64), true, nil
}

// ListCooks returns the most recent cooks (newest first), including the active
// one, with a per-cook sample count.
func (s *Store) ListCooks(limit int) ([]CookMeta, error) {
	rows, err := s.db.Query(
		`SELECT c.id, c.name, c.meat_type, c.target_celsius, c.started_at, c.ended_at,
		        c.max_tip_celsius, c.max_ambient_celsius,
		        (SELECT COUNT(*) FROM samples s WHERE s.cook_id = c.id) AS n
		 FROM cooks c ORDER BY c.started_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list cooks: %w", err)
	}
	defer rows.Close()

	var out []CookMeta
	for rows.Next() {
		var (
			m       CookMeta
			started int64
			ended   sql.NullInt64
		)
		if err := rows.Scan(&m.ID, &m.Name, &m.MeatType, &m.TargetCelsius, &started, &ended,
			&m.MaxTipCelsius, &m.MaxAmbientCelsius, &m.Samples); err != nil {
			return nil, err
		}
		m.StartedAt = time.UnixMilli(started)
		if ended.Valid {
			t := time.UnixMilli(ended.Int64)
			m.EndedAt = &t
		} else {
			m.Active = true
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// FinishedCooks returns completed cooks (newest first) with a sample count,
// used to learn a personalised time-to-target estimate from past cooks.
func (s *Store) FinishedCooks(limit int) ([]CookMeta, error) {
	rows, err := s.db.Query(
		`SELECT c.id, c.name, c.meat_type, c.target_celsius, c.started_at, c.ended_at,
		        c.max_tip_celsius, c.max_ambient_celsius,
		        (SELECT COUNT(*) FROM samples s WHERE s.cook_id = c.id) AS n
		 FROM cooks c WHERE c.ended_at IS NOT NULL
		 ORDER BY c.started_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("finished cooks: %w", err)
	}
	defer rows.Close()

	var out []CookMeta
	for rows.Next() {
		var (
			m       CookMeta
			started int64
			ended   sql.NullInt64
		)
		if err := rows.Scan(&m.ID, &m.Name, &m.MeatType, &m.TargetCelsius, &started, &ended,
			&m.MaxTipCelsius, &m.MaxAmbientCelsius, &m.Samples); err != nil {
			return nil, err
		}
		m.StartedAt = time.UnixMilli(started)
		if ended.Valid {
			t := time.UnixMilli(ended.Int64)
			m.EndedAt = &t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CookSamples returns all samples for a cook (oldest first).
func (s *Store) CookSamples(cookID int64) ([]Point, error) {
	rows, err := s.db.Query(
		`SELECT at, tip_celsius, ambient_celsius FROM samples WHERE cook_id = ? ORDER BY at ASC`,
		cookID,
	)
	if err != nil {
		return nil, fmt.Errorf("cook samples: %w", err)
	}
	defer rows.Close()

	var out []Point
	for rows.Next() {
		var (
			at       int64
			tip, amb float64
		)
		if err := rows.Scan(&at, &tip, &amb); err != nil {
			return nil, err
		}
		out = append(out, Point{At: time.UnixMilli(at), TipCelsius: tip, AmbientCelsius: amb})
	}
	return out, rows.Err()
}

// Stats summarises what the database currently holds, for telemetry.
type Stats struct {
	Cooks         int
	FinishedCooks int
	Samples       int
}

// Stats counts the retained cooks and samples. Counting samples is a full scan,
// so callers that poll (the metrics collector) should cache the result rather
// than run it on every request: the store keeps a single connection, and a scan
// of a long cook's samples would otherwise stall the writer appending readings.
func (s *Store) Stats() (Stats, error) {
	var st Stats
	if err := s.db.QueryRow(
		`SELECT COUNT(*), COUNT(ended_at) FROM cooks`,
	).Scan(&st.Cooks, &st.FinishedCooks); err != nil {
		return Stats{}, fmt.Errorf("stats cooks: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM samples`).Scan(&st.Samples); err != nil {
		return Stats{}, fmt.Errorf("stats samples: %w", err)
	}
	return st, nil
}

// Prune deletes finished cooks beyond the most recent keepEndedCooks, along
// with their samples. Active (open) cooks are always kept.
func (s *Store) Prune() error {
	const keepIDs = `SELECT id FROM cooks WHERE ended_at IS NOT NULL
		ORDER BY started_at DESC LIMIT ?`
	if _, err := s.db.Exec(
		`DELETE FROM samples WHERE cook_id IN (
			SELECT id FROM cooks WHERE ended_at IS NOT NULL AND id NOT IN (`+keepIDs+`)
		)`, keepEndedCooks,
	); err != nil {
		return fmt.Errorf("prune samples: %w", err)
	}
	if _, err := s.db.Exec(
		`DELETE FROM cooks WHERE ended_at IS NOT NULL AND id NOT IN (`+keepIDs+`)`,
		keepEndedCooks,
	); err != nil {
		return fmt.Errorf("prune cooks: %w", err)
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scanCook(row scanner) (*CookMeta, error) {
	var (
		m       CookMeta
		started int64
		ended   sql.NullInt64
	)
	if err := row.Scan(&m.ID, &m.Name, &m.MeatType, &m.TargetCelsius, &started, &ended,
		&m.MaxTipCelsius, &m.MaxAmbientCelsius); err != nil {
		return nil, err
	}
	m.StartedAt = time.UnixMilli(started)
	if ended.Valid {
		t := time.UnixMilli(ended.Int64)
		m.EndedAt = &t
	} else {
		m.Active = true
	}
	return &m, nil
}
