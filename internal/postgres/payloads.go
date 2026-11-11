package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/api-mittens/looster/internal/event"
)

// PayloadStore acknowledges only remotely committed, immutable payloads. A local
// queue alone is not sufficient because another host can immediately read a ref.
type PayloadStore interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
}

const payloadPrefix = "looster-payload:v1:"

func (s *Store) committedPayload(ctx context.Context, key string) ([]byte, error) {
	if remote, ok := s.payloads.(interface {
		GetCommitted(context.Context, string) ([]byte, error)
	}); ok {
		return remote.GetCommitted(ctx, key)
	}
	return s.payloads.Get(ctx, key)
}

func (s *Store) SetPayloadStore(p PayloadStore) { s.payloads = p }
func payloadKey(value []byte) string            { h := sha256.Sum256(value); return hex.EncodeToString(h[:]) }
func isPayloadReference(value string) bool {
	if !strings.HasPrefix(value, payloadPrefix) || len(value) != len(payloadPrefix)+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, payloadPrefix))
	return err == nil
}
func (s *Store) externalize(ctx context.Context, value string) (string, error) {
	if value == "" || s == nil || s.payloads == nil {
		return value, nil
	}
	key := payloadKey([]byte(value))
	if err := s.payloads.Put(ctx, key, []byte(value)); err != nil {
		return "", fmt.Errorf("commit lake payload: %w", err)
	}
	return payloadPrefix + key, nil
}
func (s *Store) hydrate(ctx context.Context, value string) (string, error) {
	if !isPayloadReference(value) {
		return value, nil
	}
	if s.payloads == nil {
		return "", errors.New("DuckLake payload reader is required")
	}
	key := strings.TrimPrefix(value, payloadPrefix)
	data, err := s.payloads.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("read lake payload: %w", err)
	}
	if payloadKey(data) != key {
		return "", errors.New("DuckLake payload checksum mismatch")
	}
	return string(data), nil
}
func (s *Store) hydrateStrings(ctx context.Context, values []*string) error {
	keys := []string{}
	for _, v := range values {
		if isPayloadReference(*v) {
			keys = append(keys, strings.TrimPrefix(*v, payloadPrefix))
		}
	}
	if len(keys) == 0 {
		return nil
	}
	if s.payloads == nil {
		return errors.New("DuckLake payload reader is required")
	}
	if batch, ok := s.payloads.(interface {
		GetMany(context.Context, []string) (map[string][]byte, error)
	}); ok {
		data, err := batch.GetMany(ctx, keys)
		if err != nil {
			return err
		}
		for _, v := range values {
			if isPayloadReference(*v) {
				key := strings.TrimPrefix(*v, payloadPrefix)
				b, ok := data[key]
				if !ok || payloadKey(b) != key {
					return errors.New("missing or corrupt DuckLake payload")
				}
				*v = string(b)
			}
		}
		return nil
	}
	for _, v := range values {
		var err error
		*v, err = s.hydrate(ctx, *v)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) hydrateEvents(ctx context.Context, events []event.Event) ([]event.Event, error) {
	values := make([]*string, 0, len(events))
	for i := range events {
		if events[i].Kind != "comment" {
			values = append(values, &events[i].Body)
		}
	}
	if err := s.hydrateStrings(ctx, values); err != nil {
		return nil, err
	}
	return events, nil
}
