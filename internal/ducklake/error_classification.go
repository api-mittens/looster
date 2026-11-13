package ducklake

import (
	"errors"
	"strings"

	"github.com/duckdb/duckdb-go/v2"
)

// ErrCatalogBusy identifies transient catalog contention or connection capacity
// exhaustion. Callers may retry with a bounded backoff. No native cause escapes.
var ErrCatalogBusy = errors.New("DuckLake catalog is busy")

type catalogBusyError struct{ error }

func (e catalogBusyError) Is(target error) bool { return target == ErrCatalogBusy }

// classifiedOperationError preserves the fixed stage diagnostic and exposes only
// constant categories. It deliberately does not wrap the cause: native messages
// can contain SQL, connection strings, signed URLs and object contents.
func classifiedOperationError(stage string, cause error) error {
	category := nativeErrorCategory(cause)
	if category == "" {
		return operationError(stage)
	}
	safe := errors.New(operationError(stage).Error() + " [category: " + category + "]")
	switch category {
	case "connection_limit", "transaction_conflict", "transaction_retry_exhausted":
		return catalogBusyError{safe}
	default:
		return safe
	}
}

func nativeErrorCategory(cause error) string {
	var native *duckdb.Error
	if !errors.As(cause, &native) || native == nil {
		return ""
	}
	// postgres_scanner can report these as ErrorTypeInvalid instead of a
	// structured transaction/connection type. Match fixed diagnostic markers
	// only on native errors, never on arbitrary callback/wrapper strings.
	msg := strings.ToLower(native.Msg)
	switch native.Type {
	case duckdb.ErrorTypeInvalid, duckdb.ErrorTypeIO, duckdb.ErrorTypeConnection, duckdb.ErrorTypeTransaction, duckdb.ErrorTypeFatal:
		switch {
		case strings.Contains(msg, "remaining connection slots are reserved"), strings.Contains(msg, "sorry, too many clients already"), strings.Contains(msg, "too many connections for role"), strings.Contains(msg, "too many connections for database"):
			return "connection_limit"
		case strings.Contains(msg, "exceeded the maximum retry count"):
			return "transaction_retry_exhausted"
		case strings.Contains(msg, "could not serialize access due to concurrent update"), strings.Contains(msg, "could not serialize access due to read/write dependencies among transactions"), strings.Contains(msg, "transaction conflict attempting to alter table"):
			return "transaction_conflict"
		}
	}
	// The pinned driver exposes Type and Msg, but no structured HTTP status,
	// DuckLake retry count or postgres disconnect code. Prefer Type; inspect only
	// known diagnostic markers for those extension-specific refinements. Never
	// copy any portion of Msg (or the wrapping error) into the returned error.
	switch native.Type {
	case duckdb.ErrorTypeOutOfMemory:
		return "out_of_memory"
	case duckdb.ErrorTypeTransaction:
		if strings.Contains(native.Msg, "Exceeded the maximum retry count") {
			return "transaction_retry_exhausted"
		}
		return "transaction_conflict"
	case duckdb.ErrorTypeConnection:
		return "catalog_or_engine_disconnect"
	case duckdb.ErrorTypePermission:
		return "authorization"
	case duckdb.ErrorTypeNetwork:
		return "network"
	case duckdb.ErrorTypeHTTP, duckdb.ErrorTypeIO:
		msg := strings.ToLower(native.Msg)
		if strings.Contains(msg, "s3://") {
			switch {
			case strings.Contains(msg, "403 (forbidden)"), strings.Contains(msg, "401 (unauthorized)"), strings.Contains(msg, "accessdenied"), strings.Contains(msg, "signaturedoesnotmatch"):
				return "s3_authorization"
			case strings.Contains(msg, "connection error"), strings.Contains(msg, "connection refused"), strings.Contains(msg, "connection reset"), strings.Contains(msg, "timed out"), strings.Contains(msg, "could not resolve"):
				return "s3_network"
			}
		}
		if strings.Contains(msg, "server closed the connection unexpectedly") || strings.Contains(msg, "connection to server was lost") {
			return "catalog_disconnect"
		}
		if native.Type == duckdb.ErrorTypeHTTP {
			return "http"
		}
		return "io"
	case duckdb.ErrorTypeCatalog:
		return "catalog"
	case duckdb.ErrorTypeConstraint:
		return "constraint"
	case duckdb.ErrorTypeInterrupt:
		return "interrupted"
	case duckdb.ErrorTypeFatal, duckdb.ErrorTypeInternal:
		return "engine_internal"
	case duckdb.ErrorTypeInvalidConfiguration:
		return "configuration"
	default:
		return ""
	}
}
