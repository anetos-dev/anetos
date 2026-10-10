// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
	"anetos.dev/anetos/web"
)

// jobsPage is how many failed jobs a page shows.
const jobsPage = 50

// Jobs adds the jobs pages: the queues' sizes and the failed jobs (their
// error, payload and attempts), retried or forgotten one by one or all at
// once. queues are the queues to show beyond q's default one.
// Permissions: admin.jobs.view, and admin.jobs.update to retry and
// forget, which are logged.
func Jobs(p *Panel, q *queue.Queue, queues ...string) error {
	if q == nil {
		return errors.New("admin: Jobs needs the app's queue")
	}
	return p.add(&jobsRes{p: p, q: q, queues: queueNames(q, queues), in: resInfo{Name: "jobs", Title: "Jobs", Singular: "Job", custom: []string{"view", "update"}}})
}

// queueNames returns q's default queue and others, once each.
func queueNames(q *queue.Queue, others []string) []string {
	out := []string{q.Config().Default}
	for _, n := range others {
		if n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

type jobsRes struct {
	p      *Panel
	q      *queue.Queue
	queues []string
	in     resInfo
}

func (r *jobsRes) info() *resInfo { return &r.in }

// count is the failed jobs' count (none shown for a queue that runs
// jobs at once: the sync driver's, or a custom one without a store).
func (r *jobsRes) count(ctx context.Context) (int64, error) {
	if r.q.Store() == nil {
		return -1, nil
	}
	return queue.CountFailed(ctx, r.q.Store())
}

func (r *jobsRes) url(suffix string) string { return r.p.base + "/jobs" + suffix }

func (r *jobsRes) mount(g *web.Router) {
	view := g.With(rbac.Require(r.in.perm("view")))
	update := g.With(rbac.Require(r.in.perm("view"), r.in.perm("update")))
	view.Get("/", r.index).Name("admin.jobs.index")
	view.Get("/failed/{id}", r.show).Name("admin.jobs.show")
	update.Post("/failed/{id}/retry", r.retry).Name("admin.jobs.retry")
	update.Post("/failed/{id}/forget", r.forget).Name("admin.jobs.forget")
	update.Post("/failed/retry-all", r.retryAll).Name("admin.jobs.retry-all")
	update.Post("/failed/flush", r.p.confirmFirst(r.flush)).Name("admin.jobs.flush")
}

// failedRow is a failed job in the list.
type failedRow struct {
	ID, URL, Queue, Job, Error, FailedAt string
	Attempts                             int
}

func (r *jobsRes) failedRow(c *web.Ctx, j queue.FailedJob) failedRow {
	return failedRow{ID: j.ID, URL: r.url("/failed/" + url.PathEscape(j.ID)), Queue: j.Queue, Job: j.Job(),
		Error: firstLine(j.Error, 120), FailedAt: timeText(c, j.FailedAt), Attempts: j.Attempts}
}

func firstLine(s string, n int) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > n {
		s = limitText(s, n) + "…"
	}
	return s
}

// limitText cuts s to at most n bytes, at a character boundary.
func limitText(s string, n int) string {
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:min(n, len(s))]
}

func (r *jobsRes) index(c *web.Ctx) error {
	data := struct {
		Sync      bool
		Queues    []Stat
		Failed    []failedRow
		Prev      string
		Next      string
		CanUpdate bool
		CSRF      string
		RetryAll  string
		Flush     string
	}{Sync: r.q.Store() == nil, CanUpdate: rbac.Can(c, r.in.perm("update")), CSRF: csrfToken(c),
		RetryAll: r.url("/failed/retry-all"), Flush: r.url("/failed/flush")}
	if !data.Sync {
		for _, name := range r.queues {
			n, err := r.q.Store().Size(c, name)
			if err != nil {
				return err
			}
			data.Queues = append(data.Queues, Stat{Label: name, Value: thousands(n), Hint: "waiting or running"})
		}
		pageNo, _ := strconv.Atoi(c.Query("page"))
		pageNo = max(pageNo, 1)
		jobs, err := r.q.Store().Failed(c, (pageNo-1)*jobsPage, jobsPage+1)
		if err != nil {
			return err
		}
		if len(jobs) > jobsPage {
			data.Next = web.PageURL(c, pageNo+1)
			jobs = jobs[:jobsPage]
		}
		if pageNo > 1 {
			data.Prev = web.PageURL(c, pageNo-1)
		}
		for _, j := range jobs {
			data.Failed = append(data.Failed, r.failedRow(c, j))
		}
	}
	return r.p.render(c, "jobs", page{Title: "Jobs", Crumbs: []navItem{{Title: r.p.cfg.Title, URL: r.p.URL()}}, Data: data})
}

// find returns the failed job of the path.
func (r *jobsRes) find(c *web.Ctx) (queue.FailedJob, error) {
	j, ok, err := queue.FindFailed(c, r.q.Store(), c.Param("id"))
	if err != nil {
		return queue.FailedJob{}, err
	}
	if !ok {
		return queue.FailedJob{}, db.ErrNotFound
	}
	return j, nil
}

func (r *jobsRes) show(c *web.Ctx) error {
	if r.q.Store() == nil {
		return db.ErrNotFound
	}
	j, err := r.find(c)
	if err != nil {
		return err
	}
	data := struct {
		Job       failedRow
		Error     string
		Payload   string
		CanUpdate bool
		CSRF      string
		Retry     string
		Forget    string
	}{Job: r.failedRow(c, j), Error: j.Error, Payload: string(j.Payload), CanUpdate: rbac.Can(c, r.in.perm("update")),
		CSRF: csrfToken(c), Retry: r.url("/failed/" + url.PathEscape(j.ID) + "/retry"), Forget: r.url("/failed/" + url.PathEscape(j.ID) + "/forget")}
	title := "Failed job " + j.Job()
	return r.p.render(c, "job", page{Title: title, Crumbs: []navItem{{Title: r.p.cfg.Title, URL: r.p.URL()},
		{Title: "Jobs", URL: r.url("")}, {Title: title}}, Data: data})
}

// failedJobs is the failed jobs' subject type in the audit log (not a
// table's, so the entries link nowhere).
const failedJobs = "queue:failed"

// jobSubject is a failed job's subject in the audit log.
func jobSubject(id string) audit.Subject {
	return audit.Subject{Type: failedJobs, ID: limitText(id, 255)}
}

func (r *jobsRes) retry(c *web.Ctx) error {
	if r.q.Store() == nil { // a queue that runs jobs at once fails none
		return db.ErrNotFound
	}
	id := c.Param("id")
	ok, err := r.q.Store().Retry(c, id)
	if err != nil {
		return err
	}
	if !ok {
		return failed(c, "There is no such failed job any more.", r.url(""))
	}
	if err := record(c, "job.retried", jobSubject(id), nil); err != nil {
		return err
	}
	return done(c, "The job is back on its queue.", r.url(""))
}

func (r *jobsRes) forget(c *web.Ctx) error {
	if r.q.Store() == nil { // a queue that runs jobs at once fails none
		return db.ErrNotFound
	}
	id := c.Param("id")
	ok, err := r.q.Store().Forget(c, id)
	if err != nil {
		return err
	}
	if !ok {
		return failed(c, "There is no such failed job any more.", r.url(""))
	}
	if err := record(c, "job.forgotten", jobSubject(id), nil); err != nil {
		return err
	}
	return done(c, "The failed job is forgotten.", r.url(""))
}

func (r *jobsRes) retryAll(c *web.Ctx) error {
	if r.q.Store() == nil { // a queue that runs jobs at once fails none
		return db.ErrNotFound
	}
	// The failed jobs now: those that fail meanwhile wait for next time.
	var ids []string
	for offset := 0; ; offset += 500 {
		jobs, err := r.q.Store().Failed(c, offset, 500)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			ids = append(ids, j.ID)
		}
		if len(jobs) < 500 {
			break
		}
	}
	n := 0
	for _, id := range ids {
		ok, err := r.q.Store().Retry(c, id)
		if err != nil {
			return err
		}
		if ok {
			n++
		}
	}
	if err := record(c, "jobs.retried", audit.Subject{Type: failedJobs}, map[string]any{"jobs": n}); err != nil {
		return err
	}
	return done(c, fmt.Sprintf("%d failed jobs are back on their queues.", n), r.url(""))
}

func (r *jobsRes) flush(c *web.Ctx) error {
	if r.q.Store() == nil { // a queue that runs jobs at once fails none
		return db.ErrNotFound
	}
	n, err := r.q.Store().Flush(c)
	if err != nil {
		return err
	}
	if err := record(c, "jobs.flushed", audit.Subject{Type: failedJobs}, map[string]any{"jobs": n}); err != nil {
		return err
	}
	return done(c, fmt.Sprintf("%d failed jobs forgotten.", n), r.url(""))
}

// QueueHealth is a widget of queue q: the jobs waiting on its default
// queue and on queues, and the failed ones, with a link to the jobs pages
// ([Jobs]). Its permission is admin.jobs.view.
func QueueHealth(q *queue.Queue, queues ...string) Widget {
	return Widget{Title: "Queue", Permission: "admin.jobs.view", Load: func(ctx context.Context) (Content, error) {
		if q.Store() == nil {
			return Content{Stats: []Stat{{Label: "Jobs", Value: "Run at once", Hint: "QUEUE_DRIVER=sync, or a driver without a store"}}}, nil
		}
		var c Content
		for _, name := range queueNames(q, queues) {
			n, err := q.Store().Size(ctx, name)
			if err != nil {
				return Content{}, err
			}
			c.Stats = append(c.Stats, Stat{Label: name, Value: thousands(n), Hint: "waiting or running"})
		}
		failed, err := q.Store().Failed(ctx, 0, 1000)
		if err != nil {
			return Content{}, err
		}
		f := Stat{Label: "Failed", Value: thousands(int64(len(failed))), Hint: "kept to retry", Warn: len(failed) > 0}
		if len(failed) == 1000 {
			f.Value = "1,000+"
		}
		c.Stats = append(c.Stats, f)
		c.Link = &Link{Label: "The failed jobs", URL: "jobs"}
		return c, nil
	}}
}

// AIUsage is a widget of the app's AI calls (ai.TrackUsage): tokens and
// cost in the last 24 hours and 30 days, and tokens a day for 30 days
// (UTC). Its permission is admin.access.
func AIUsage() Widget {
	return Widget{Title: "AI usage", Load: func(ctx context.Context) (Content, error) {
		type sum struct {
			Tokens float64 `db:"tokens"`
			Cost   float64 `db:"cost"`
			Calls  int64   `db:"calls"`
		}
		since := func(d time.Duration) (sum, error) {
			rows, err := db.Select[sum](db.Query[ai.UsageRecord](ctx).Where(db.C("created_at").Gte(anetos.Now(ctx).Add(-d))),
				"COALESCE(SUM(input_tokens + output_tokens), 0) AS tokens", "COALESCE(SUM(cost), 0) AS cost", "COUNT(*) AS calls")
			if err != nil || len(rows) == 0 {
				return sum{}, err
			}
			return rows[0], nil
		}
		day, err := since(24 * time.Hour)
		if err != nil {
			return Content{}, err
		}
		month, err := since(30 * 24 * time.Hour)
		if err != nil {
			return Content{}, err
		}
		bars, err := perDay(ctx, db.Query[ai.UsageRecord](ctx), "created_at", 30, "SUM(input_tokens + output_tokens)")
		if err != nil {
			return Content{}, err
		}
		cost := func(f float64) string { return "$" + strconv.FormatFloat(f, 'f', 2, 64) }
		return Content{Stats: []Stat{
			{Label: "Tokens", Value: number(day.Tokens), Hint: thousands(day.Calls) + " calls in 24 hours"},
			{Label: "Cost", Value: cost(day.Cost), Hint: "in 24 hours"},
			{Label: "Tokens", Value: number(month.Tokens), Hint: thousands(month.Calls) + " calls in 30 days"},
			{Label: "Cost", Value: cost(month.Cost), Hint: "in 30 days"},
		}, Bars: bars}, nil
	}}
}

// Schedule adds the scheduled tasks page: each task's schedule, next and
// last run (Scheduler.LastRun), and running one now, in the background.
// Permissions: admin.schedule.view, and admin.schedule.run to run one,
// which is logged. The app's shutdown waits for the runs started here,
// until its deadline, when their context is canceled.
func Schedule(p *Panel, s *schedule.Scheduler) error {
	if s == nil {
		return errors.New("admin: Schedule needs the app's scheduler")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &scheduleRes{p: p, s: s, ctx: ctx, running: map[string]bool{},
		in: resInfo{Name: "schedule", Title: "Scheduled tasks", Singular: "Task", custom: []string{"view", "run"}}}
	if err := p.add(r); err != nil {
		cancel()
		return err
	}
	p.app.OnShutdown("admin.schedule", func(sctx context.Context) error {
		defer cancel()
		done := make(chan struct{})
		go func() {
			r.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			return nil
		case <-sctx.Done():
			return fmt.Errorf("admin: scheduled tasks run from the admin: %w", sctx.Err())
		}
	})
	return nil
}

type scheduleRes struct {
	p  *Panel
	s  *schedule.Scheduler
	in resInfo

	ctx     context.Context // the runs', canceled at shutdown
	wg      sync.WaitGroup
	mu      sync.Mutex
	running map[string]bool // the tasks running from here
}

func (r *scheduleRes) info() *resInfo { return &r.in }

func (r *scheduleRes) count(context.Context) (int64, error) { return int64(len(r.s.Tasks())), nil }

func (r *scheduleRes) mount(g *web.Router) {
	g.With(rbac.Require("admin.schedule.view")).Get("/", r.index).Name("admin.schedule.index")
	g.With(rbac.Require("admin.schedule.view", "admin.schedule.run")).Post("/{name}/run", r.run).Name("admin.schedule.run")
}

// taskRow is a task in the list.
type taskRow struct {
	Name, Schedule, Next, Last, LastError, Options, RunURL string
	Failed, Running                                        bool
}

func (r *scheduleRes) index(c *web.Ctx) error {
	data := struct {
		Tasks  []taskRow
		CanRun bool
		CSRF   string
	}{CanRun: rbac.Can(c, "admin.schedule.run"), CSRF: csrfToken(c)}
	now := anetos.Now(c)
	for _, t := range r.s.Tasks() {
		row := taskRow{Name: t.Name, Schedule: t.Schedule.String(), Next: timeText(c, t.Next(now)),
			RunURL: r.p.base + "/schedule/" + url.PathEscape(t.Name) + "/run", Last: "Not known"}
		var opts []string
		if t.WithoutOverlapping {
			opts = append(opts, "without overlapping")
		}
		if t.OnOneServer {
			opts = append(opts, "on one server")
		}
		if t.Timeout > 0 {
			opts = append(opts, "timeout "+t.Timeout.String())
		}
		row.Options = strings.Join(opts, ", ")
		// The last run is kept in the cache: without one (or with it
		// down), it isn't known, and the page still works.
		run, ok, err := r.s.LastRun(c, t.Name)
		if err != nil {
			r.p.app.Logger().WarnContext(c, "admin: a task's last run", "task", t.Name, "error", err)
		} else if ok {
			row.Last = timeText(c, run.At) + " (" + run.Duration.String() + ")"
			row.LastError, row.Failed = run.Error, run.Error != ""
		}
		r.mu.Lock()
		row.Running = r.running[t.Name]
		r.mu.Unlock()
		data.Tasks = append(data.Tasks, row)
	}
	return r.p.render(c, "schedule", page{Title: "Scheduled tasks", Crumbs: []navItem{{Title: r.p.cfg.Title, URL: r.p.URL()}}, Data: data})
}

func (r *scheduleRes) run(c *web.Ctx) error {
	name := c.Param("name")
	if !slices.ContainsFunc(r.s.Tasks(), func(t schedule.TaskInfo) bool { return t.Name == name }) {
		return db.ErrNotFound
	}
	back := r.p.base + "/schedule"
	r.mu.Lock()
	if r.running[name] {
		r.mu.Unlock()
		return failed(c, "The task "+name+" is already running from here.", back)
	}
	r.running[name] = true
	r.mu.Unlock()
	if err := record(c, "schedule.run", audit.Subject{Type: "schedule:tasks", ID: limitText(name, 255)}, nil); err != nil {
		r.mu.Lock()
		delete(r.running, name)
		r.mu.Unlock()
		return err
	}
	// In the background, as the scheduler runs it (not as the admin): a
	// task may take longer than a request should.
	r.wg.Go(func() {
		defer func() {
			r.mu.Lock()
			delete(r.running, name)
			r.mu.Unlock()
		}()
		log := r.p.app.Logger()
		switch err := r.s.RunTask(r.ctx, name); {
		case errors.Is(err, schedule.ErrOverlap):
			log.WarnContext(r.ctx, "admin: a task run from the admin was already running", "task", name)
		case err != nil:
			log.ErrorContext(r.ctx, "admin: running a task", "task", name, "error", err)
		}
	})
	return done(c, "The task "+name+" is running. Its last run shows here once it ends.", back)
}
