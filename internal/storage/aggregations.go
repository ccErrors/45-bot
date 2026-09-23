package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Aggregation is a user's group of races rendered together on one map.
// Removing a race from an aggregation never deletes the race itself.
type Aggregation struct {
	ID        int64
	UserID    int64
	CreatedAt time.Time
}

// AggregationSummary is a row of the user's aggregation list.
type AggregationSummary struct {
	Aggregation
	Races          int
	FirstStartedAt time.Time
	LastPointAt    time.Time
}

// CreateAggregation stores a new aggregation of the given races.
func (s *Store) CreateAggregation(ctx context.Context, userID int64, raceIDs []int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO aggregations (user_id, created_at) VALUES (?, ?)`, userID, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, rid := range raceIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO aggregation_races (aggregation_id, race_id) VALUES (?, ?)`, id, rid); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

func (s *Store) GetAggregation(ctx context.Context, id int64) (*Aggregation, error) {
	var (
		a       Aggregation
		created int64
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, created_at FROM aggregations WHERE id = ?`, id).
		Scan(&a.ID, &a.UserID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.CreatedAt = time.Unix(created, 0)
	return &a, nil
}

// AggregationRaces returns the aggregation's races in chronological order.
func (s *Store) AggregationRaces(ctx context.Context, aggID int64) ([]*Race, error) {
	return s.queryRaces(ctx, `SELECT `+prefixed("r.", raceCols)+` FROM races r
		JOIN aggregation_races ar ON ar.race_id = r.id
		WHERE ar.aggregation_id = ?
		ORDER BY r.started_at, r.id`, aggID)
}

// ListAggregations returns the user's latest aggregations, newest first.
func (s *Store) ListAggregations(ctx context.Context, userID int64, limit int) ([]AggregationSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.user_id, a.created_at, COUNT(r.id), COALESCE(MIN(r.started_at), 0), COALESCE(MAX(r.last_point_at), 0)
		FROM aggregations a
		LEFT JOIN aggregation_races ar ON ar.aggregation_id = a.id
		LEFT JOIN races r ON r.id = ar.race_id
		WHERE a.user_id = ?
		GROUP BY a.id
		ORDER BY a.id DESC
		LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AggregationSummary
	for rows.Next() {
		var (
			a                    AggregationSummary
			created, first, last int64
		)
		if err := rows.Scan(&a.ID, &a.UserID, &created, &a.Races, &first, &last); err != nil {
			return nil, err
		}
		a.CreatedAt, a.FirstStartedAt, a.LastPointAt = time.Unix(created, 0), time.Unix(first, 0), time.Unix(last, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddAggregationRace adds a race; adding a race that is already there is a no-op.
func (s *Store) AddAggregationRace(ctx context.Context, aggID, raceID int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO aggregation_races (aggregation_id, race_id) VALUES (?, ?)`, aggID, raceID)
	return err
}

func (s *Store) RemoveAggregationRace(ctx context.Context, aggID, raceID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM aggregation_races WHERE aggregation_id = ? AND race_id = ?`, aggID, raceID)
	return err
}

// DeleteAggregation removes the aggregation; its races stay untouched.
func (s *Store) DeleteAggregation(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM aggregations WHERE id = ?`, id)
	return err
}

// NeighborRaces returns up to n of the user's races started right before
// `before` and right after `after` (chronologically, ties broken by id),
// skipping the excluded ids. prev is ordered nearest first, next likewise.
func (s *Store) NeighborRaces(ctx context.Context, userID int64, before, after *Race, n int, exclude map[int64]bool) (prev, next []*Race, err error) {
	// Fetch extra rows so excluded ones don't shrink the result.
	limit := n + len(exclude)
	prevAll, err := s.queryRaces(ctx, `SELECT `+raceCols+` FROM races
		WHERE user_id = ? AND (started_at < ? OR (started_at = ? AND id < ?))
		ORDER BY started_at DESC, id DESC LIMIT ?`,
		userID, before.StartedAt.Unix(), before.StartedAt.Unix(), before.ID, limit)
	if err != nil {
		return nil, nil, err
	}
	nextAll, err := s.queryRaces(ctx, `SELECT `+raceCols+` FROM races
		WHERE user_id = ? AND (started_at > ? OR (started_at = ? AND id > ?))
		ORDER BY started_at, id LIMIT ?`,
		userID, after.StartedAt.Unix(), after.StartedAt.Unix(), after.ID, limit)
	if err != nil {
		return nil, nil, err
	}
	pick := func(rs []*Race) []*Race {
		var out []*Race
		for _, r := range rs {
			if !exclude[r.ID] && len(out) < n {
				out = append(out, r)
			}
		}
		return out
	}
	return pick(prevAll), pick(nextAll), nil
}

// RaceAggregations lists the aggregations containing the race, with their size.
func (s *Store) RaceAggregations(ctx context.Context, raceID int64) ([]AggregationSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.user_id, a.created_at, (SELECT COUNT(*) FROM aggregation_races x WHERE x.aggregation_id = a.id)
		FROM aggregations a JOIN aggregation_races ar ON ar.aggregation_id = a.id
		WHERE ar.race_id = ?
		ORDER BY a.id`, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AggregationSummary
	for rows.Next() {
		var (
			a       AggregationSummary
			created int64
		)
		if err := rows.Scan(&a.ID, &a.UserID, &created, &a.Races); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(created, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}
