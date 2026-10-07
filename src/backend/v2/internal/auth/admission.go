package auth

import "context"

type kdfAdmission struct {
	running chan struct{}
	waiting chan struct{}
}

func newKDFAdmission(maxConcurrent, maxQueued int) *kdfAdmission {
	return &kdfAdmission{
		running: make(chan struct{}, maxConcurrent),
		waiting: make(chan struct{}, maxQueued),
	}
}

func (a *kdfAdmission) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	select {
	case a.running <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-a.running
			return nil, err
		}
		return a.releaseRunning, nil
	default:
	}

	select {
	case a.waiting <- struct{}{}:
	default:
		return nil, ErrKDFSaturated
	}

	select {
	case a.running <- struct{}{}:
		<-a.waiting
		if err := ctx.Err(); err != nil {
			<-a.running
			return nil, err
		}
		return a.releaseRunning, nil
	case <-ctx.Done():
		<-a.waiting
		return nil, ctx.Err()
	}
}

func (a *kdfAdmission) releaseRunning() { <-a.running }
