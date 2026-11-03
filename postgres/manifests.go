package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dominikbb/looster/app"
	"github.com/dominikbb/looster/behaviour"
	"github.com/jackc/pgx/v5"
)

var _ app.ManifestRepository = (*Store)(nil)

func lockManifest(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':looster:manifests',0))`)
	return err
}

func (s *Store) ApplyManifest(ctx context.Context, source string) (behaviour.Manifest, error) {
	rules, err := behaviour.Parse(source)
	if err != nil {
		return behaviour.Manifest{}, err
	}
	m := behaviour.Manifest{Source: source, Rules: rules, Active: true}
	data, err := json.Marshal(rules)
	if err != nil {
		return behaviour.Manifest{}, err
	}
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		if err := lockManifest(ctx, tx); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO behaviour_manifests(source,rules) VALUES($1,$2) RETURNING id,created_at`, source, data).Scan(&m.ID, &m.CreatedAt); err != nil {
			return err
		}
		return s.replaceManifest(ctx, tx, m.ID, data)
	})
	if err != nil {
		return behaviour.Manifest{}, err
	}
	return m, nil
}

func (s *Store) replaceManifest(ctx context.Context, tx pgx.Tx, id int64, data []byte) error {
	q := s.q.WithTx(tx)
	// Keep durable agent identities and their sessions/memories, but omitted
	// definitions must no longer contribute stale instructions.
	if _, err := tx.Exec(ctx, `UPDATE agents SET description='' WHERE NOT EXISTS (
 SELECT FROM jsonb_to_recordset($1::jsonb) AS d(trigger text,target text)
 WHERE d.trigger IN ('agent','prompt') AND d.target=agents.name)`, data); err != nil {
		return err
	}
	if err := q.UpsertPromptAgents(ctx, data); err != nil {
		return err
	}
	// Tags retain their identity, task assignments, and memories across versions.
	if _, err := tx.Exec(ctx, `UPDATE tags SET manifest_active=false`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tags(name,description,manifest_active)
 SELECT target,coalesce(prompt,''),true FROM jsonb_to_recordset($1::jsonb) AS d(trigger text,target text,prompt text) WHERE trigger='tag'
 ON CONFLICT(name) DO UPDATE SET description=excluded.description,manifest_active=true`, data); err != nil {
		return err
	}
	if err := q.RemoveAbsentRules(ctx, data); err != nil {
		return err
	}
	if err := q.AddNewRules(ctx, data); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO behaviour_manifest_state(singleton,active_id) VALUES(true,$1) ON CONFLICT(singleton) DO UPDATE SET active_id=excluded.active_id`, id)
	return err
}

func (s *Store) ActivateManifest(ctx context.Context, id int64) error {
	if id < 1 {
		return errors.New("manifest id must be positive")
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if err := lockManifest(ctx, tx); err != nil {
			return err
		}
		var data []byte
		if err := tx.QueryRow(ctx, `SELECT rules FROM behaviour_manifests WHERE id=$1`, id).Scan(&data); err != nil {
			return fmt.Errorf("manifest %d: %w", id, repositoryError(err))
		}
		return s.replaceManifest(ctx, tx, id, data)
	})
}

func (s *Store) Manifests(ctx context.Context) ([]behaviour.Manifest, error) {
	out := []behaviour.Manifest{}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT m.id,m.source,m.rules,coalesce(m.id=s.active_id,false),m.created_at FROM behaviour_manifests m LEFT JOIN behaviour_manifest_state s ON s.singleton ORDER BY m.id DESC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m behaviour.Manifest
			var data []byte
			if err := rows.Scan(&m.ID, &m.Source, &data, &m.Active, &m.CreatedAt); err != nil {
				return err
			}
			if err := json.Unmarshal(data, &m.Rules); err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

// Compatibility callers also apply complete files, never incremental changes.
func (s *Store) CreateBehaviour(ctx context.Context, rules []behaviour.Rule) error {
	if len(rules) == 0 {
		return errors.New("no behaviour statements to apply")
	}
	return s.SetBehaviour(ctx, rules)
}
func (s *Store) SetBehaviour(ctx context.Context, rules []behaviour.Rule) error {
	for _, r := range rules {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	_, err := s.ApplyManifest(ctx, behaviour.Format(rules))
	return err
}
func (s *Store) Behaviour(ctx context.Context) ([]behaviour.Rule, error) {
	out := []behaviour.Rule{}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var data []byte
		err := tx.QueryRow(ctx, `SELECT m.rules FROM behaviour_manifests m JOIN behaviour_manifest_state s ON s.active_id=m.id WHERE s.singleton`).Scan(&data)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &out)
	})
	return out, err
}
