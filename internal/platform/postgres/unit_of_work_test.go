package postgres

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestUnitOfWorkRejectsInvalidConfiguration(t *testing.T) {
	var uow unitOfWork
	if err := uow.WithinTransaction(context.Background(), pgx.TxOptions{}, func(context.Context, pgx.Tx) error { return nil }); err == nil {
		t.Fatal("WithinTransaction() error = nil, want missing pool error")
	}
}
