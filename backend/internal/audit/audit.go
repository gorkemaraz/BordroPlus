// Package audit writes and reads the append-only audit trail. Writers pass
// the same transaction that performs the change, so an action and its audit
// record commit or roll back together.
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"bordroplus/internal/db"
)

// Entry is one audit record. Old and New are marshalled to JSON; nil stores NULL.
type Entry struct {
	ActorUserID *int64
	ActorRole   string
	Action      string // dotted lower-case verb, e.g. "auth.login"
	EntityType  string
	EntityID    string
	Old, New    any
	RequestID   string
	IP          string
}

// Write appends e using q (normally the caller's transaction).
func Write(ctx context.Context, q db.Querier, e Entry) error {
	oldJSON, err := marshal(e.Old)
	if err != nil {
		return err
	}
	newJSON, err := marshal(e.New)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO audit_log (actor_user_id, actor_role, action, entity_type, entity_id, old_value, new_value, request_id, ip)
		VALUES ($1, NULLIF($2, ''), $3, $4, NULLIF($5, ''), $6, $7, NULLIF($8, ''), NULLIF($9, '')::inet)`,
		e.ActorUserID, e.ActorRole, e.Action, e.EntityType, e.EntityID, oldJSON, newJSON, e.RequestID, e.IP)
	if err != nil {
		return fmt.Errorf("audit %s: %w", e.Action, err)
	}
	return nil
}

func marshal(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("audit marshal: %w", err)
	}
	return b, nil
}
