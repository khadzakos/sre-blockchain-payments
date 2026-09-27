package repository

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/khadzakos/sre-blockchain-payments/internal/telemetry"
)

type (
	queryTracer struct{}
	queryKey    struct{}
)

func (queryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	operation := "query"
	if words := strings.Fields(data.SQL); len(words) > 0 {
		operation = strings.ToLower(words[0])
	}
	ctx, finish := telemetry.Start(ctx, "postgres."+operation)
	// SQL arguments and statements are deliberately excluded from logs/traces.
	return context.WithValue(ctx, queryKey{}, finish)
}

func (queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if finish, ok := ctx.Value(queryKey{}).(func(error)); ok {
		finish(data.Err)
	}
}
