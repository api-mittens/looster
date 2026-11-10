package postgres

import (
	"context"
	"errors"
	"fmt"
)

// EnablePayloadCutover durably fences legacy inline payload writers in this
// namespace. Deploy payload-capable readers first and configure a durable
// PayloadStore on every writer before calling it. Existing inline values are
// retained for BackfillPayloads and metadata-only updates remain possible.
//
// Every payload write takes a SHARE lock on the singleton fence row. Updating
// that row drains in-flight inline writers and makes subsequent writes observe
// the enabled flag, even on old connections or transactions. There is no
// disable API or automatic destructive cleanup.
func (s *Store) EnablePayloadCutover(ctx context.Context) error {
	if s == nil || s.payloads == nil {
		return errors.New("payload store is required before enabling payload cutover")
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE payload_cutover SET enabled=true WHERE singleton`)
	if err != nil {
		return fmt.Errorf("enable payload cutover: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("payload cutover fence is missing")
	}
	return nil
}
