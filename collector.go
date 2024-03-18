package taskgroup

import "sync"

// A Collector collects values reported by task functions and delivers them to
// an accumulator function.
type Collector[T any] struct {
	fn func(T)
	mu sync.Mutex
}

// NewCollector creates a new collector that delivers task values to the
// specified accumulator function. The collector serializes calls to value, so
// that it is safe for the function to access shared state without a lock.  The
// caller must call Wait when the collector is no longer needed, even if it has
// not been used.
func NewCollector[T any](value func(T)) *Collector[T] {
	return &Collector[T]{fn: value}
}

// Wait stops the collector and blocks until it has finished processing.
// It is safe to call Wait multiple times from a single goroutine.
// Note that after Wait has been called, c is no longer valid.
func (c *Collector[T]) Wait() {
}

func (c *Collector[T]) collect(value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fn(value)
}

// Task returns a Task wrapping a call to f. If f reports an error, that error
// is propagated as the return value of the task; otherwise, the non-error
// value reported by f is passed to the value callback.
func (c *Collector[T]) Task(f func() (T, error)) Task {
	return func() error {
		v, err := f()
		if err != nil {
			return err
		}
		c.collect(v)
		return nil
	}
}

// Stream returns a task wrapping a call to f, which is passed a channel on
// which results can be sent to the accumulator.
//
// Note: f must not close its argument channel.
func (c *Collector[T]) Stream(f func(chan<- T) error) Task {
	return func() error {
		ch := make(chan T)
		sink := Go(NoError(func() {
			for v := range ch {
				c.collect(v)
			}
		}))
		defer func() {
			// f has completed, no new values will be
			// generated. Signal sink and wait for it to drain any
			// remaining value.
			close(ch)
			sink.Wait()
		}()
		return f(ch)
	}
}

func (c *Collector[T]) Report(f func(func(T)) error) Task {
	return func() error { return f(c.collect) }
}

// NoError returns a Task wrapping a call to f. The resulting task reports a
// nil error for all calls.
func (c *Collector[T]) NoError(f func() T) Task {
	return NoError(func() { c.collect(f()) })
}
