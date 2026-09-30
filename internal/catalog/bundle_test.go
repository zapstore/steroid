package catalog

import (
	"context"
	"errors"
	"testing"
)

func TestProcessListingsLimit(t *testing.T) {
	const limit = 4
	const jobs = 10
	work := make([]Listing, jobs)
	for i := range work {
		work[i].AppID = string(rune('a' + i))
	}
	entered := make(chan int, jobs)
	release := make(chan struct{})
	errc := make(chan error, 1)
	go func() {
		errc <- processListings(context.Background(), work, limit, func(ctx context.Context, n int, _ Listing) error {
			entered <- n
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	got := map[int]struct{}{}
	for len(got) < limit {
		got[<-entered] = struct{}{}
	}
	select {
	case n := <-entered:
		t.Fatalf("job %d started past the limit", n)
	default:
	}
	close(release)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	total := len(got)
	for len(entered) > 0 {
		<-entered
		total++
	}
	if total != jobs {
		t.Fatalf("finished %d jobs, want %d", total, jobs)
	}
}

func TestProcessListingsStopsOnError(t *testing.T) {
	const limit = 4
	const jobs = 16
	work := make([]Listing, jobs)
	for i := range work {
		work[i].AppID = string(rune('a' + i))
	}
	started := make(chan struct{}, jobs)
	err := processListings(context.Background(), work, limit, func(ctx context.Context, n int, _ Listing) error {
		started <- struct{}{}
		if n == 1 {
			return errors.New("boom")
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v", err)
	}
	if got := len(started); got == 0 || got > limit {
		t.Fatalf("started %d, limit %d", got, limit)
	}
}
