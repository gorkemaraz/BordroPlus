package audit

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/httpx"
)

// Record is an audit row as returned by the API.
type Record struct {
	ID          int64           `json:"id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	ActorUserID *int64          `json:"actor_user_id"`
	ActorRole   *string         `json:"actor_role"`
	Action      string          `json:"action"`
	EntityType  string          `json:"entity_type"`
	EntityID    *string         `json:"entity_id"`
	OldValue    json.RawMessage `json:"old_value"`
	NewValue    json.RawMessage `json:"new_value"`
	RequestID   *string         `json:"request_id"`
	IP          *string         `json:"ip"`
}

// ListHandler serves GET /audit-log, newest first, with keyset pagination
// (?cursor=<id of the last row seen>) and optional entity_type, entity_id,
// actor_user_id and action filters. Authorisation is applied by the router.
func ListHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		limit, err := httpx.PageLimit(r, 50, 200)
		if err != nil {
			return err
		}
		var cursor, actor *int64
		for name, dst := range map[string]**int64{"cursor": &cursor, "actor_user_id": &actor} {
			if v := q.Get(name); v != "" {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil || n < 1 {
					return httpx.Validation(map[string]string{name: "pozitif tam sayı olmalı"})
				}
				*dst = &n
			}
		}

		rows, err := pool.Query(r.Context(), `
			SELECT id, occurred_at, actor_user_id, actor_role, action, entity_type, entity_id,
			       old_value, new_value, request_id, host(ip)
			  FROM audit_log
			 WHERE ($1::bigint IS NULL OR id < $1)
			   AND (NULLIF($2, '') IS NULL OR entity_type = $2)
			   AND (NULLIF($3, '') IS NULL OR entity_id = $3)
			   AND ($4::bigint IS NULL OR actor_user_id = $4)
			   AND (NULLIF($5, '') IS NULL OR action = $5)
			 ORDER BY id DESC
			 LIMIT $6`,
			cursor, q.Get("entity_type"), q.Get("entity_id"), actor, q.Get("action"), limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()

		page := httpx.Page[Record]{Items: make([]Record, 0, limit)}
		for rows.Next() {
			var rec Record
			if err := rows.Scan(&rec.ID, &rec.OccurredAt, &rec.ActorUserID, &rec.ActorRole, &rec.Action,
				&rec.EntityType, &rec.EntityID, &rec.OldValue, &rec.NewValue, &rec.RequestID, &rec.IP); err != nil {
				return err
			}
			page.Items = append(page.Items, rec)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(page.Items) > limit {
			page.Items = page.Items[:limit]
			page.NextCursor = strconv.FormatInt(page.Items[limit-1].ID, 10)
		}
		httpx.WriteJSON(w, http.StatusOK, page)
		return nil
	})
}
