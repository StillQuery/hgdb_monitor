package service

import (
	"context"

	"hgdb_monitor/core/internal/inspector"
	"hgdb_monitor/core/internal/model"
)

// Current performs a read-only, non-persisted collection for the local
// management page. The internal datasource id never leaves the loopback API.
func (r *Runtime) Current(ctx context.Context, id string) (model.Snapshot, error) {
	release, err := r.acquire(ctx, id)
	if err != nil {
		return model.Snapshot{}, err
	}
	defer release()
	_, config, err := r.connectionConfig(ctx, id)
	if err != nil {
		return model.Snapshot{}, err
	}
	snapshot, err := r.collect(ctx, config)
	if err != nil {
		return model.Snapshot{}, err
	}
	if r.collectStatus != nil {
		status, statusErr := r.collectStatus(ctx, config)
		if statusErr != nil {
			return model.Snapshot{}, statusErr
		}
		model.ApplyStatus(&snapshot, status)
	}
	return snapshot, nil
}

func (r *Runtime) Activities(ctx context.Context, id string, kind inspector.ActivityKind, limit int) ([]inspector.Activity, error) {
	release, err := r.acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	_, config, err := r.connectionConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	return inspector.Activities(ctx, config, kind, limit)
}

func (r *Runtime) LockWaits(ctx context.Context, id string, limit int) ([]inspector.LockWait, error) {
	release, err := r.acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	_, config, err := r.connectionConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	return inspector.LockWaits(ctx, config, limit)
}

func (r *Runtime) AIImmediate(ctx context.Context, instanceRef string) (model.Snapshot, error) {
	source, err := r.resolveInstanceRef(ctx, instanceRef)
	if err != nil {
		return model.Snapshot{}, err
	}
	release, err := r.acquire(ctx, source.ID)
	if err != nil {
		return model.Snapshot{}, err
	}
	defer release()
	_, config, err := r.connectionConfig(ctx, source.ID)
	if err != nil {
		return model.Snapshot{}, err
	}
	snapshot, err := r.collect(ctx, config)
	if err != nil {
		return model.Snapshot{}, err
	}
	if r.collectStatus != nil {
		status, statusErr := r.collectStatus(ctx, config)
		if statusErr != nil {
			return model.Snapshot{}, statusErr
		}
		model.ApplyStatus(&snapshot, status)
	}
	return snapshot, nil
}

func (r *Runtime) AIActivities(ctx context.Context, instanceRef string, kind inspector.ActivityKind, limit int) ([]inspector.Activity, error) {
	return r.AIActivitiesWithMinimum(ctx, instanceRef, kind, 0, limit)
}

func (r *Runtime) AIActivitiesWithMinimum(ctx context.Context, instanceRef string, kind inspector.ActivityKind, minimumSeconds, limit int) ([]inspector.Activity, error) {
	source, err := r.resolveInstanceRef(ctx, instanceRef)
	if err != nil {
		return nil, err
	}
	release, err := r.acquire(ctx, source.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	_, config, err := r.connectionConfig(ctx, source.ID)
	if err != nil {
		return nil, err
	}
	return inspector.ActivitiesWithMinimum(ctx, config, kind, minimumSeconds, limit)
}

func (r *Runtime) AILockWaits(ctx context.Context, instanceRef string, limit int) ([]inspector.LockWait, error) {
	source, err := r.resolveInstanceRef(ctx, instanceRef)
	if err != nil {
		return nil, err
	}
	release, err := r.acquire(ctx, source.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	_, config, err := r.connectionConfig(ctx, source.ID)
	if err != nil {
		return nil, err
	}
	return inspector.LockWaits(ctx, config, limit)
}

func (r *Runtime) AIExplain(ctx context.Context, instanceRef, sql string) (inspector.ExplainResult, error) {
	source, err := r.resolveInstanceRef(ctx, instanceRef)
	if err != nil {
		return inspector.ExplainResult{}, err
	}
	release, err := r.acquire(ctx, source.ID)
	if err != nil {
		return inspector.ExplainResult{}, err
	}
	defer release()
	_, config, err := r.connectionConfig(ctx, source.ID)
	if err != nil {
		return inspector.ExplainResult{}, err
	}
	return inspector.Explain(ctx, config, sql)
}
