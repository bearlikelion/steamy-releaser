package service

import (
	"context"
	"errors"
	"fmt"
)

// ErrProjectBusy indicates that another Steam Releaser operation has locked
// the same project. The lock is cooperative; external build tools do not use it.
var ErrProjectBusy = errors.New("another build or upload is already running for this project")

func acquireProjectLock(ctx context.Context, projectRoot string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	releaseLock, err := tryProjectLock(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("lock project %s: %w", projectRoot, err)
	}
	if err := ctx.Err(); err != nil {
		releaseLock()
		return nil, err
	}
	return releaseLock, nil
}
