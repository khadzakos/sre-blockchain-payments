package worker

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/khadzakos/sre-blockchain-payments/internal/service"
	"github.com/khadzakos/sre-blockchain-payments/internal/telemetry"
)

func Run(ctx context.Context, s *service.Invoices, interval time.Duration, metrics *telemetry.Metrics) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		check, cancel := context.WithTimeout(ctx, 10*time.Second)
		check, finish := telemetry.Start(check, "worker.expire")
		count, err := s.ExpireDue(check)
		if err != nil {
			metrics.WorkerRuns.WithLabelValues("error").Inc()
		} else {
			metrics.WorkerRuns.WithLabelValues("success").Inc()
			metrics.WorkerSuccess.SetToCurrentTime()
		}
		if count > 0 {
			telemetry.Logger(check).Info("invoices expired", zap.Int("count", count))
		}
		finish(err)
		cancel()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
