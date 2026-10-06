// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
	"anetos.dev/anetos/view"
)

// opsApp is an admin with a dashboard, the activity, the jobs and the
// scheduled tasks, on a memory queue. Its task "slow" runs until hold is
// closed (or the app stops).
func opsApp(t *testing.T, ran chan<- string, hold <-chan struct{}, env ...string) *anetostest.App {
	t.Helper()
	vars := map[string]string{"QUEUE_DRIVER": "memory"}
	for i := 0; i+1 < len(env); i += 2 {
		vars[env[i]] = env[i+1]
	}
	return anetostest.New(t, setupWith(func(p *Panel) error {
		q, err := queue.ForApp(p.app)
		if err != nil {
			return err
		}
		s, err := schedule.ForApp(p.app)
		if err != nil {
			return err
		}
		if err := s.Add(schedule.DailyAt("03:00"), "prune", func(context.Context) error {
			ran <- "prune"
			return errors.New("disk full")
		}, schedule.WithoutOverlapping()); err != nil {
			return err
		}
		if err := s.Add(schedule.Hourly(), "slow", func(ctx context.Context) error {
			select {
			case <-hold:
			case <-ctx.Done():
			}
			return nil
		}); err != nil {
			return err
		}
		mine := Widget{Title: "Mine", Load: func(context.Context) (Content, error) {
			return Content{Stats: []Stat{{Label: "Orders", Value: "12", Warn: true}},
				Bars:  []Bar{{"Mon", 1}, {"Tue", 0}, {"Wed", 4}},
				Table: &Table{Headers: []string{"Order", "Total"}, Rows: [][]string{{"#1", "<b>$5</b>"}}, Links: []string{"posts"}},
				HTML: view.ComponentFunc(func(_ context.Context, w io.Writer) error {
					_, err := io.WriteString(w, "<p class=mine>custom</p>")
					return err
				}),
				Link: &Link{Title: "Gone", URL: "nowhere"}}, nil
		}}
		broken := Widget{Title: "Broken", Load: func(context.Context) (Content, error) { return Content{}, errors.New("boom") }}
		panics := Widget{Title: "Panics", Load: func(context.Context) (Content, error) { panic("oops") }}
		odd := Widget{Title: "Odd", Load: func(context.Context) (Content, error) {
			return Content{Bars: []Bar{{"a", -3}, {"b", math.NaN()}, {"c", 2}}}, nil
		}}
		hidden := Widget{Title: "Secret", Permission: "admin.jobs.update", Load: func(context.Context) (Content, error) { return Content{}, nil }}
		if err := errors.Join(Activity(p), Jobs(p, q), Schedule(p, s)); err != nil {
			return err
		}
		return p.Dashboard(mine, broken, panics, odd, hidden, SignUps[User]("Users", "created_at"), QueueHealth(q), AIUsage(), RecentActivity(5))
	}), anetostest.Env(vars))
}

func TestDashboard(t *testing.T) {
	app := opsApp(t, make(chan string, 1), nil)
	signIn(t, app, "Ada", "admin")
	newPost(t, app, "First", "draft")
	if err := db.Create(app.Context(), &ai.UsageRecord{UserID: "1", Provider: "fake", Model: "m", InputTokens: 1000, OutputTokens: 234, Cost: 0.5}); err != nil {
		t.Fatal(err)
	}
	page := app.Get("/admin").AssertOK().AssertSee(
		"Mine", "Orders", `class="stat warn"`, `<rect x="20" y="0" width="8" height="60"><title>Wed: 4</title>`, "&lt;b&gt;$5&lt;/b&gt;",
		`<a href="/admin/posts">#1</a>`, "<p class=mine>custom</p>",
		"Broken", "It couldn&#39;t be loaded.", "Panics",
		`<rect x="0" y="60" width="8" height="0"><title>a: 0</title>`, `<title>b: 0</title>`, `<rect x="20" y="0" width="8" height="60"><title>c: 2</title>`,
		"Users", "In all", "in 7 days",
		"Queue", "Failed", `href="/admin/jobs"`,
		"AI usage", "1,234", "$0.50",
		"Recent activity", "created", "posts #1",
		"Activity", "Jobs", "Scheduled tasks").
		AssertDontSee("Gone").Text()
	if strings.Count(page, "<rect") < 30+3+3+30 {
		t.Error("missing bars")
	}
	if strings.Count(page, "It couldn&#39;t be loaded.") != 2 {
		t.Error("the panicking widget isn't shown as failed")
	}

	p := anetos.MustResolve[*Panel](app.App)
	if err := p.Dashboard(Widget{Title: "Late", Load: func(context.Context) (Content, error) { return Content{}, nil }}); err == nil {
		t.Error("Dashboard after Mount")
	}
}

func TestDashboardErrors(t *testing.T) {
	ok := func(context.Context) (Content, error) { return Content{}, nil }
	for name, w := range map[string]Widget{
		"no title":    {Load: ok},
		"no load":     {Title: "X"},
		"undeclared":  {Title: "X", Permission: "reports.view", Load: ok},
		"admin's own": {Title: "X", Permission: "admin.reports.view", Load: ok},
	} {
		t.Run(name, func(t *testing.T) {
			var got error
			anetostest.New(t, setupWith(func(p *Panel) error {
				got = p.Dashboard(w)
				return nil
			}))
			if got == nil {
				t.Error("no error")
			}
		})
	}
}

// Without the activity pages, recent activity links nowhere.
func TestRecentActivityAlone(t *testing.T) {
	app := anetostest.New(t, setupWith(func(p *Panel) error { return p.Dashboard(RecentActivity(5)) }))
	signIn(t, app, "Ada", "admin")
	newPost(t, app, "First", "draft")
	app.Get("/admin").AssertOK().AssertSee("Recent activity", "posts #1").AssertDontSee("/admin/activity")
}

func TestActivityPages(t *testing.T) {
	app := opsApp(t, make(chan string, 1), nil)
	ada := signIn(t, app, "Ada", "admin")
	p := newPost(t, app, "First", "draft")
	app.PostForm(postURL(p, ""), url.Values{"title": {"Renamed"}, "status": {"draft"}}).AssertStatus(303)
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"feature"}, "ids": {fmt.Sprint(p.ID)}}).AssertStatus(303)

	// The record's history, on its page.
	app.Get(postURL(p, "")).AssertSee("History", "updated", "created", "bulk, 1 rows", "Ada")
	app.Get("/admin/activity").AssertOK().AssertSee("updated", "posts #"+fmt.Sprint(p.ID), "Ada", "request POST")
	app.Get("/admin/activity?action=updated&type=posts&id=" + fmt.Sprint(p.ID)).AssertSee("updated").AssertDontSee("<code>created</code>")
	app.Get("/admin/activity?who=user:" + ada.AuthID() + "&action=created").AssertSee("Nothing matches.")
	app.Get("/admin/activity?from=2000-01-01&to=2000-01-02").AssertSee("Nothing matches.")

	e, err := db.Query[audit.Entry](app.Context()).Where(db.C("action").Eq("updated")).First()
	if err != nil {
		t.Fatal(err)
	}
	app.Get(fmt.Sprintf("/admin/activity/%d", e.ID)).AssertOK().AssertSee("Changes", "title", "First", "Renamed", "its history")
	app.Get("/admin/activity/999999").AssertNotFound()
	o, err := db.Query[audit.BulkOp](app.Context()).First()
	if err != nil {
		t.Fatal(err)
	}
	app.Get("/admin/activity?kind=bulk").AssertSee("1 rows").AssertDontSee("Newest")
	// A bulk write is found by a record it touched.
	app.Get("/admin/activity?kind=bulk&type=posts&id=" + fmt.Sprint(p.ID)).AssertSee("1 rows")
	app.Get("/admin/activity?kind=bulk&id=999999").AssertSee("Nothing matches.")
	// A later page leads back to the first, keeping the filters.
	app.Get(fmt.Sprintf("/admin/activity?kind=bulk&type=posts&before=%d", o.ID+1)).AssertSee(`href="?kind=bulk&amp;type=posts">Newest`)
	app.Get(fmt.Sprintf("/admin/activity?action=updated&before=%d", e.ID+1)).AssertSee(`href="?action=updated">Newest`)
	app.Get(fmt.Sprintf("/admin/activity/bulk/%d", o.ID)).AssertOK().AssertSee("Rows", `href="/admin/posts/`+fmt.Sprint(p.ID)+`"`, "featured")

	// Without the permission: nothing of it.
	signIn(t, app, "Vera", Access, "admin.posts.view")
	app.Get("/admin/activity").AssertForbidden()
	app.Get(postURL(p, "")).AssertOK().AssertDontSee("<h2>History</h2>")
	app.Get("/admin").AssertDontSee("Recent activity", "Queue", "Secret")
}

func TestJobsPage(t *testing.T) {
	app := opsApp(t, make(chan string, 1), nil)
	signIn(t, app, "Ada", "admin")
	q := anetos.MustResolve[*queue.Queue](app.App)
	ctx := app.Context()
	fail := func(id string) {
		t.Helper()
		st := q.Store()
		if err := st.Push(ctx, queue.Message{ID: id, Queue: "default", Payload: []byte(`{"id":"` + id + `","job":"send-welcome","data":{"user_id":7}}`)}, 0); err != nil {
			t.Fatal(err)
		}
		r, err := st.Reserve(ctx, "default", time.Minute)
		if err != nil || r == nil {
			t.Fatal(err)
		}
		if err := st.Fail(ctx, r, "smtp: connection refused\nmore"); err != nil {
			t.Fatal(err)
		}
	}
	fail("j1")
	fail("j2")
	app.Get("/admin").AssertSee(`<a href="/admin/jobs"><span class="count">2</span><span>Jobs</span></a>`)
	app.Get("/admin/jobs").AssertOK().AssertSee("send-welcome", "smtp: connection refused", "Retry all", "Forget all",
		`action="/admin/jobs/failed/retry-all"`, `action="/admin/jobs/failed/flush"`)
	app.Get("/admin/jobs/failed/j1").AssertOK().AssertSee("user_id", "more")
	app.Get("/admin/jobs/failed/nope").AssertNotFound()
	app.PostForm("/admin/jobs/failed/j1/retry", nil).AssertRedirect("/admin/jobs").Follow().AssertSee("The job is back on its queue.")
	if n, _ := q.Store().Size(ctx, "default"); n != 1 {
		t.Errorf("%d waiting", n)
	}
	app.PostForm("/admin/jobs/failed/j1/retry", nil).Follow().AssertSee("There is no such failed job any more.")
	app.PostForm("/admin/jobs/failed/j2/forget", nil).Follow().AssertSee("The failed job is forgotten.", "No failed jobs.")
	fail("j3")
	app.PostForm("/admin/jobs/failed/retry-all", nil).Follow().AssertSee("1 failed jobs are back on their queues.")
	fail("j4")
	app.PostForm("/admin/jobs/failed/flush", nil).Follow().AssertSee("1 failed jobs forgotten.")
	var actions []string
	es, _ := db.Query[audit.Entry](ctx).Where(db.C("subject_type").Eq("queue:failed")).OrderBy(db.C("id").Asc()).Get()
	for _, e := range es {
		actions = append(actions, e.Action)
	}
	if fmt.Sprint(actions) != "[job.retried job.forgotten jobs.retried jobs.flushed]" {
		t.Errorf("logged %v", actions)
	}
	signIn(t, app, "Vera", Access, "admin.jobs.view")
	app.Get("/admin/jobs").AssertOK().AssertDontSee("Retry all")
	app.PostForm("/admin/jobs/failed/flush", nil).AssertForbidden()
}

// With the admin on a host of its own, the forms post to it.
func TestJobsHost(t *testing.T) {
	app := opsApp(t, make(chan string, 1), nil, "ADMIN_HOST", "admin.example.com")
	q := anetos.MustResolve[*queue.Queue](app.App)
	ctx := app.Context()
	if err := q.Store().Push(ctx, queue.Message{ID: "j1", Queue: "default", Payload: []byte(`{"id":"j1","job":"x"}`)}, 0); err != nil {
		t.Fatal(err)
	}
	r, err := q.Store().Reserve(ctx, "default", time.Minute)
	if err != nil || r == nil {
		t.Fatal(err)
	}
	if err := q.Store().Fail(ctx, r, "boom"); err != nil {
		t.Fatal(err)
	}
	signIn(t, app, "Ada", "admin")
	req, err := http.NewRequest(http.MethodGet, "/jobs", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "admin.example.com"
	app.Do(req).AssertOK().AssertSee(`action="/jobs/failed/retry-all"`, `action="/jobs/failed/flush"`, `href="/jobs/failed/j1"`)
}

func TestSchedulePage(t *testing.T) {
	ran := make(chan string, 1)
	hold := make(chan struct{})
	app := opsApp(t, ran, hold)
	signIn(t, app, "Ada", "admin")
	app.Get("/admin/schedule").AssertOK().AssertSee("prune", "without overlapping", "Not known", "Run now")
	app.PostForm("/admin/schedule/prune/run", nil).AssertRedirect("/admin/schedule").Follow().AssertSee("The task prune is running.")
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the task didn't run")
	}
	s := anetos.MustResolve[*schedule.Scheduler](app.App)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok, _ := s.LastRun(app.Context(), "prune"); ok || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	app.Get("/admin/schedule").AssertSee("Failed: disk full")
	app.PostForm("/admin/schedule/nope/run", nil).AssertNotFound()
	var entries int64
	if entries, _ = db.Query[audit.Entry](app.Context()).Where(db.C("subject_type").Eq("schedule:tasks"), db.C("action").Eq("schedule.run")).Count(); entries != 1 {
		t.Errorf("%d entries", entries)
	}

	// A task running from here isn't run again until it ends.
	app.PostForm("/admin/schedule/slow/run", nil).Follow().AssertSee("The task slow is running.")
	app.Get("/admin/schedule").AssertSee("Running now, from here")
	app.PostForm("/admin/schedule/slow/run", nil).Follow().AssertSee("The task slow is already running from here.")
	close(hold)

	// A last run that can't be read isn't known; the page works.
	if err := cache.Set(app.Context(), "schedule:last:prune", "garbage", time.Hour); err != nil {
		t.Fatal(err)
	}
	app.Get("/admin/schedule").AssertOK().AssertSee("Not known").AssertDontSee("disk full")
	signIn(t, app, "Vera", Access, "admin.schedule.view")
	app.Get("/admin/schedule").AssertOK().AssertDontSee("Run now")
	app.PostForm("/admin/schedule/prune/run", nil).AssertForbidden()
}

func TestNumbers(t *testing.T) {
	for f, want := range map[float64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -1500: "-1,500", 1.5: "1.50"} {
		if got := number(f); got != want {
			t.Errorf("number(%v) = %q", f, got)
		}
	}
	if d := days(time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC), 3); d[0].Day() != 4 || d[2].Day() != 6 {
		t.Errorf("days %v", d)
	}
}
