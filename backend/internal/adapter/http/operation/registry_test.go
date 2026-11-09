package operation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/adapter/http/operation"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestRegistry_BeginAndCancelOperation(t *testing.T) {
	t.Parallel()

	reg := operation.NewRegistry()
	ctx, release := reg.Begin(context.Background(), "op-123")
	defer release()

	if !reg.IsActive("op-123") {
		t.Fatal("expected op-123 to be active")
	}

	canceled := reg.Cancel("op-123")
	if !canceled {
		t.Fatal("expected Cancel(op-123) to return true")
	}

	if err := context.Cause(ctx); !errors.Is(err, entity.ErrOperationCanceled) {
		t.Fatalf("expected context cause ErrOperationCanceled, got %v", err)
	}

	if reg.IsActive("op-123") {
		t.Fatal("expected op-123 to no longer be active after cancel")
	}

	if reg.Cancel("non-existent") {
		t.Fatal("expected Cancel(non-existent) to return false")
	}
}
