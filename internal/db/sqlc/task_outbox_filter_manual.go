package sqlc

import "context"

// ListTaskOutboxFiltered is the bounded operator diagnostic projection used by
// Charlie. Filtering happens before LIMIT/OFFSET so pagination is stable even
// when a task type is selected.
type ListTaskOutboxFilteredParams struct {
	Status   string `json:"status"`
	TaskType string `json:"task_type"`
	Limit    int32  `json:"limit"`
	Offset   int32  `json:"offset"`
}

const listTaskOutboxFiltered = `
SELECT ` + taskOutboxSelectColumns + `
FROM task_outbox
WHERE ($1::text = '' OR status = $1)
  AND ($2::text = '' OR task_type = $2)
ORDER BY updated_at DESC, created_at DESC
LIMIT $3 OFFSET $4`

func (q *Queries) ListTaskOutboxFiltered(ctx context.Context, arg ListTaskOutboxFilteredParams) ([]TaskOutbox, error) {
	rows, err := q.db.Query(ctx, listTaskOutboxFiltered, arg.Status, arg.TaskType, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaskOutbox{}
	for rows.Next() {
		item, err := scanTaskOutboxRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
