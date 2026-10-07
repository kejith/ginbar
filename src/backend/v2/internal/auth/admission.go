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

func (a *kdfAdmission) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	select {
	case a.running <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-a.running
			return err
		}
		return nil
	default:
	}

	select {
	case a.waiting <- struct{}{}:
	default:
		return ErrKDFSaturated
	}

	select {
	case a.running <- struct{}{}:
		<-a.waiting
		if err := ctx.Err(); err != nil {
			<-a.running
			return err
		}
		return nil
	case <-ctx.Done():
		<-a.waiting
		return ctx.Err()
	}
}

func (a *kdfAdmission) release() { <-a.running }
