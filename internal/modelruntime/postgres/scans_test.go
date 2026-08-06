package postgres

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

type strictNullScanner struct {
	values []any
}

func (s strictNullScanner) Scan(dest ...any) error {
	if len(dest) != len(s.values) {
		return fmt.Errorf("destination count %d does not match values %d", len(dest), len(s.values))
	}
	for index, value := range s.values {
		target := reflect.ValueOf(dest[index])
		if target.Kind() != reflect.Pointer || target.IsNil() {
			return fmt.Errorf("destination %d is not a writable pointer", index)
		}
		target = target.Elem()
		if value == nil {
			switch target.Kind() {
			case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
				target.Set(reflect.Zero(target.Type()))
				continue
			default:
				return fmt.Errorf("cannot scan NULL into %s", target.Type())
			}
		}
		source := reflect.ValueOf(value)
		if source.Type().AssignableTo(target.Type()) {
			target.Set(source)
			continue
		}
		if source.Type().ConvertibleTo(target.Type()) {
			target.Set(source.Convert(target.Type()))
			continue
		}
		if target.Kind() == reflect.Pointer && source.Type().AssignableTo(target.Type().Elem()) {
			pointer := reflect.New(target.Type().Elem())
			pointer.Elem().Set(source)
			target.Set(pointer)
			continue
		}
		return fmt.Errorf("cannot assign %T to %s", value, target.Type())
	}
	return nil
}

func TestScanInvocationAcceptsNullableTextColumns(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	value, err := scanInvocation(strictNullScanner{values: []any{
		int64(1), "explorarte", int64(7), int64(2), int64(3),
		"ingenieria_ia/qa", "ingenieria_ia/qa", nil, nil, int64(4), "test",
		"worker-default", int64(5), "test.fake", "deterministic-v1",
		int64(17), hash, int64(23), hash, []byte(`[]`), modelruntime.OutputText, nil, 128, nil,
		modelruntime.ThinkingDisabled, "idempotency", hash,
		modelruntime.InvocationRequested, nil, nil, now, nil, nil, now, now, nil,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if value.ErrorCode != "" || value.CorrelationID != "" || value.CausationID != "" {
		t.Fatalf("nullable text was not normalized: %+v", value)
	}
}

func TestScanAttemptAcceptsNullableTextColumns(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	value, err := scanAttempt(strictNullScanner{values: []any{
		int64(1), int64(2), 1, modelruntime.DispatchClaimed, "worker-1", nil, nil, nil, nil,
		now, now.Add(time.Minute), nil, nil, nil,
		modelruntime.RetrySafeBeforeSend, nil, nil, now, nil,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if value.ProviderRequestID != "" || value.OutcomeClassification != "" || value.ErrorCode != "" {
		t.Fatalf("nullable text was not normalized: %+v", value)
	}
}
