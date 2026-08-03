package scannertrace

import (
	"context"
	"database/sql"
	"errors"
	"reflect"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type correlationReader interface {
	GetOperationCorrelation(
		context.Context,
		string,
		string,
	) (*scannerrelease.OperationCorrelation, error)
}

// Resume restores the immutable correlation assigned when a durable aggregate
// was created. Legacy/test stores without correlation support receive a fresh
// local context; a real persistence error is returned so production workers
// do not silently lose trace continuity.
func Resume(
	ctx context.Context,
	store any,
	aggregateType, aggregateID, component string,
) (context.Context, Correlation, error) {
	reader, ok := store.(correlationReader)
	if !ok || hasNilEmbeddedCorrelationReader(store) {
		resumed, value := Ensure(ctx, component)
		return resumed, value, nil
	}
	stored, err := reader.GetOperationCorrelation(ctx, aggregateType, aggregateID)
	if errors.Is(err, sql.ErrNoRows) {
		resumed, value := Ensure(ctx, component)
		return resumed, value, nil
	}
	if err != nil {
		return ctx, Correlation{}, err
	}
	value := Normalize(Correlation{
		TraceID:           stored.TraceID,
		OperationID:       stored.OperationID,
		ParentOperationID: stored.ParentOperationID,
		Component:         component,
	}, component)
	return With(ctx, value), value, nil
}

var correlationReaderType = reflect.TypeOf((*correlationReader)(nil)).Elem()

// Some persistence-neutral test doubles embed a nil aggregate interface to
// inherit a large method set and override only methods used by the test. Such
// a value technically satisfies correlationReader, but invoking its promoted
// method would dereference the nil embedded interface.
func hasNilEmbeddedCorrelationReader(store any) bool {
	value := reflect.ValueOf(store)
	if !value.IsValid() {
		return false
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return true
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false
	}
	typ := value.Type()
	for index := 0; index < value.NumField(); index++ {
		fieldType := typ.Field(index)
		field := value.Field(index)
		if !fieldType.Anonymous || field.Kind() != reflect.Interface ||
			!fieldType.Type.Implements(correlationReaderType) {
			continue
		}
		if field.IsNil() {
			return true
		}
	}
	return false
}
