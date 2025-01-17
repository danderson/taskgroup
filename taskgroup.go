// Package taskgroup manages collections of cooperating goroutines.
// It defines a Group that handles waiting for goroutine termination and the
// propagation of error values. The caller may provide a callback to filter
// and respond to task errors.
package taskgroup

import "sync"

// A Task function is the basic unit of work in a Group. Errors reported by
// tasks are collected and reported by the group.
type Task func() error

// A Group manages a collection of cooperating goroutines.  New tasks are added
// to the group with the Go method.  Call the Wait method to wait for the tasks
// to complete. A zero value is ready for use, but must not be copied after its
// first use.
//
// The group collects any errors returned by the tasks in the group. The first
// non-nil error reported by any task (and not otherwise filtered) is returned
// from the Wait method.
type Group struct {
	wg  sync.WaitGroup // counter for active goroutines
	err error          // error returned from Wait

	setup sync.Once // set up and start the error collector
	reset sync.Once // stop the error collector and set err

	onError func(error) error // called each time a task returns non-nil
	errc    chan<- error      // errors generated by goroutines
	edone   chan struct{}     // signals error completion
}

// New constructs a new empty group.  If ef != nil, it is called for each error
// reported by a task running in the group.  The value returned by ef replaces
// the task's error. If ef == nil, errors are not filtered.
//
// Calls to ef are issued by a single goroutine, so it is safe for ef to
// manipulate local data structures without additional locking.
func New(ef ErrorFunc) *Group { return &Group{onError: ef} }

// Go runs task in a new goroutine in g, and returns g to permit chaining.
func (g *Group) Go(task Task) *Group {
	g.wg.Add(1)
	g.init()
	errc := g.errc
	go func() {
		defer g.wg.Done()
		if err := task(); err != nil {
			errc <- err
		}
	}()
	return g
}

func (g *Group) init() {
	// The first time a task is added to an otherwise clear group, set up the
	// error collector goroutine.  We don't do this in the constructor so that
	// an unused group can be abandoned without orphaning a goroutine.
	g.setup.Do(func() {
		if g.onError == nil {
			g.onError = func(e error) error { return e }
		}
		g.err = nil
		g.edone = make(chan struct{})
		g.reset = sync.Once{}

		errc := make(chan error)
		g.errc = errc
		go func() {
			defer close(g.edone)
			for err := range errc {
				e := g.onError(err)
				if e != nil && g.err == nil {
					g.err = e // capture the first error always
				}
			}
		}()
	})
}

func (g *Group) cleanup() {
	g.reset.Do(func() {
		g.wg.Wait()
		if g.errc == nil {
			return
		}
		close(g.errc)
		<-g.edone
		g.errc = nil
		g.setup = sync.Once{}
	})
}

// Wait blocks until all the goroutines currently active in the group have
// returned, and all reported errors have been delivered to the callback.
// It returns the first non-nil error returned by any of the goroutines in the
// group and not filtered by an ErrorFunc.
//
// It is safe to call Wait concurrently from multiple goroutines, but as with
// sync.WaitGroup no tasks can be added to g while any call to Wait is in
// progress. Once all Wait calls have returned, the group is ready for reuse.
func (g *Group) Wait() error { g.cleanup(); return g.err }

// An ErrorFunc is called by a group each time a task reports an error.  Its
// return value replaces the reported error, so the ErrorFunc can filter or
// suppress errors by modifying or discarding the input error.
type ErrorFunc func(error) error

// Trigger creates an ErrorFunc that calls f each time a task reports an error.
// The resulting ErrorFunc returns task errors unmodified.
func Trigger(f func()) ErrorFunc { return func(e error) error { f(); return e } }

// Listen creates an ErrorFunc that reports each non-nil task error to f.  The
// resulting ErrorFunc returns task errors unmodified.
func Listen(f func(error)) ErrorFunc { return func(e error) error { f(e); return e } }

// NoError adapts f to a Task that executes f and reports a nil error.
func NoError(f func()) Task { return func() error { f(); return nil } }

// Limit returns g and a function that starts each task passed to it in g,
// allowing no more than n tasks to be active concurrently.  If n ≤ 0, the
// start function is equivalent to g.Go, which enforces no limit.
//
// The limiting mechanism is optional, and the underlying group is not
// restricted. A call to the start function will block until a slot is
// available, but calling g.Go directly will add a task unconditionally and
// will not take up a limiter slot.
func (g *Group) Limit(n int) (*Group, func(Task) *Group) {
	if n <= 0 {
		return g, g.Go
	}
	adm := make(chan struct{}, n)
	return g, func(task Task) *Group {
		adm <- struct{}{}
		return g.Go(func() error {
			defer func() { <-adm }()
			return task()
		})
	}
}
