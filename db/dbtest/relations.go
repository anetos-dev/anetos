// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

func init() {
	extra = append(extra,
		test{"RelationsEagerLoad", testRelationsEagerLoad},
		test{"RelationsLoad", testRelationsLoad},
		test{"RelationsWhereHas", testRelationsWhereHas},
		test{"RelationsPivotWrites", testRelationsPivotWrites},
		test{"RelationsErrors", testRelationsErrors},
		test{"RelationsLarge", testRelationsLarge},
	)
}

var relationTables = []string{"st_r_post_tags", "st_r_comments", "st_r_profiles", "st_r_posts", "st_r_tags", "st_r_users"}

// The relation fixtures: users write posts, have one profile and may have
// a manager (another user); posts have comments and tags.

type stRUser struct {
	ID        int64  `db:"id,pk"`
	Name      string `db:"name"`
	ManagerID *int64 `db:"manager_id"`
	db.SoftDeletes

	Posts   []stRPost   `rel:"has_many,fk=user_id"`
	Profile *stRProfile `rel:"has_one,fk=user_id"`
	Manager *stRUser    `rel:"belongs_to"`
	Reports []stRUser   `rel:"has_many,fk=manager_id"`
}

// TableName names the table: all dbtest tables start with st_.
func (stRUser) TableName() string { return "st_r_users" }

type stRProfile struct {
	ID     int64  `db:"id,pk"`
	UserID int64  `db:"user_id"`
	Bio    string `db:"bio"`
}

// TableName names the table.
func (stRProfile) TableName() string { return "st_r_profiles" }

type stRPost struct {
	ID     int64  `db:"id,pk"`
	UserID int64  `db:"user_id"`
	Title  string `db:"title"`

	User     *stRUser     `rel:"belongs_to" json:"user,omitempty"`
	Comments []stRComment `rel:"has_many,fk=post_id" json:"comments"`
	Tags     []stRTag     `rel:"many_to_many,pivot=st_r_post_tags,fk=post_id,related_fk=tag_id" json:"tags,omitempty"`
}

// TableName names the table.
func (stRPost) TableName() string { return "st_r_posts" }

type stRComment struct {
	ID       int64  `db:"id,pk"`
	PostID   int64  `db:"post_id"`
	UserID   *int64 `db:"user_id"` // nil: anonymous
	Body     string `db:"body"`
	Approved bool   `db:"approved"`

	Post   *stRPost `rel:"belongs_to"`
	Author *stRUser `rel:"belongs_to,fk=user_id"`
}

// TableName names the table.
func (stRComment) TableName() string { return "st_r_comments" }

type stRTag struct {
	ID    int64     `db:"id,pk"`
	Name  string    `db:"name"`
	Posts []stRPost `rel:"many_to_many,pivot=st_r_post_tags,fk=tag_id,related_fk=post_id"`
}

// TableName names the table.
func (stRTag) TableName() string { return "st_r_tags" }

var (
	userPosts     = db.RelOf[stRUser, stRPost]("Posts")
	userProfile   = db.RelOf[stRUser, stRProfile]("Profile")
	userManager   = db.RelOf[stRUser, stRUser]("Manager")
	userReports   = db.RelOf[stRUser, stRUser]("Reports")
	postUser      = db.RelOf[stRPost, stRUser]("User")
	postComments  = db.RelOf[stRPost, stRComment]("Comments")
	postTags      = db.RelOf[stRPost, stRTag]("Tags")
	commentAuthor = db.RelOf[stRComment, stRUser]("Author")
	tagPosts      = db.RelOf[stRTag, stRPost]("Posts")

	approved = db.Col[bool]("approved")
	name     = db.Col[string]("name")
)

// seedRelations creates the tables and this data:
//
//	users: 1 ada, 2 grace (manager: ada), 3 linus (manager: ada, soft-deleted), 4 alan (no posts)
//	profiles: ada, grace
//	posts: 1 "go" (ada), 2 "sql" (ada), 3 "html" (grace), 4 "kernel" (linus)
//	comments: on 1: by grace (approved), by linus, anonymous (approved); on 3: by ada
//	tags: 1 go, 2 web, 3 db (unused); post 1: go, web; post 2: go, db…
func seedRelations(t *testing.T, ctx context.Context) {
	t.Helper()
	dropRelationTables(ctx)
	t.Cleanup(func() { dropRelationTables(context.WithoutCancel(ctx)) })
	s, err := migrate.NewSchema(ctx)
	check(t, err)
	check(t, s.Create("st_r_users", func(t *migrate.Table) {
		t.ID()
		t.String("name", 50)
		t.ForeignID("manager_id").Nullable()
		t.SoftDeletes()
	}))
	check(t, s.Create("st_r_profiles", func(t *migrate.Table) {
		t.ID()
		t.ForeignID("user_id")
		t.String("bio", 100)
	}))
	check(t, s.Create("st_r_posts", func(t *migrate.Table) {
		t.ID()
		t.ForeignID("user_id")
		t.String("title", 50)
	}))
	check(t, s.Create("st_r_comments", func(t *migrate.Table) {
		t.ID()
		t.ForeignID("post_id")
		t.ForeignID("user_id").Nullable()
		t.String("body", 100)
		t.Boolean("approved").Default(false)
	}))
	check(t, s.Create("st_r_tags", func(t *migrate.Table) {
		t.ID()
		t.String("name", 50)
	}))
	check(t, s.Create("st_r_post_tags", func(t *migrate.Table) {
		t.ForeignID("post_id")
		t.ForeignID("tag_id")
		t.Primary("post_id", "tag_id")
	}))

	ada := int64(1)
	users := []stRUser{{Name: "ada"}, {Name: "grace", ManagerID: &ada}, {Name: "linus", ManagerID: &ada}, {Name: "alan"}}
	for i := range users {
		check(t, db.Create(ctx, &users[i]))
	}
	check(t, db.Delete(ctx, &users[2])) // linus is soft-deleted
	for _, p := range []stRProfile{{UserID: 1, Bio: "first programmer"}, {UserID: 2, Bio: "compilers"}} {
		check(t, db.Create(ctx, &p))
	}
	for _, p := range []stRPost{{UserID: 1, Title: "go"}, {UserID: 1, Title: "sql"}, {UserID: 2, Title: "html"}, {UserID: 3, Title: "kernel"}} {
		check(t, db.Create(ctx, &p))
	}
	grace, linus := int64(2), int64(3)
	for _, c := range []stRComment{
		{PostID: 1, UserID: &grace, Body: "nice", Approved: true},
		{PostID: 1, UserID: &linus, Body: "meh"},
		{PostID: 1, Body: "anon", Approved: true},
		{PostID: 3, UserID: &ada, Body: "tidy"},
	} {
		check(t, db.Create(ctx, &c))
	}
	for _, tag := range []stRTag{{Name: "go"}, {Name: "web"}, {Name: "db"}} {
		check(t, db.Create(ctx, &tag))
	}
	for _, link := range [][2]int{{1, 1}, {1, 2}, {2, 1}, {3, 2}} {
		mustExec(t, ctx, "INSERT INTO st_r_post_tags (post_id, tag_id) VALUES (?, ?)", link[0], link[1])
	}
}

func dropRelationTables(ctx context.Context) {
	for _, table := range relationTables {
		_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS "+table)
	}
}

func titles(ps []stRPost) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Title
	}
	return out
}

func tagNames(ts []stRTag) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func testRelationsEagerLoad(t *testing.T, ctx context.Context) {
	seedRelations(t, ctx)

	posts, err := db.Query[stRPost](ctx).OrderBy(id.Asc()).
		With(postUser, postComments.With(commentAuthor), postTags).
		Get()
	check(t, err)
	if len(posts) != 4 {
		t.Fatalf("posts = %d", len(posts))
	}
	go1, sql2, html3, kernel4 := posts[0], posts[1], posts[2], posts[3]

	// belongs_to, shared between posts of one user; soft-deleted owners
	// are left out.
	if go1.User == nil || go1.User.Name != "ada" || sql2.User != go1.User {
		t.Errorf("belongs_to: %+v %+v", go1.User, sql2.User)
	}
	if html3.User == nil || html3.User.Name != "grace" || kernel4.User != nil {
		t.Errorf("belongs_to, trashed owner: %+v %+v", html3.User, kernel4.User)
	}

	// has_many in primary key order, empty (not nil) when there are none,
	// with a nested belongs_to on a nullable key.
	if len(go1.Comments) != 3 || go1.Comments[0].Body != "nice" || go1.Comments[2].Body != "anon" {
		t.Fatalf("has_many: %+v", go1.Comments)
	}
	if a := go1.Comments[0].Author; a == nil || a.Name != "grace" {
		t.Errorf("nested belongs_to: %+v", a)
	}
	if go1.Comments[1].Author != nil || go1.Comments[2].Author != nil {
		t.Error("nested belongs_to on a trashed or NULL key isn't nil")
	}
	if sql2.Comments == nil || len(sql2.Comments) != 0 {
		t.Errorf("no comments: %#v", sql2.Comments)
	}

	// many_to_many.
	if got := tagNames(go1.Tags); !slices.Equal(got, []string{"go", "web"}) {
		t.Errorf("many_to_many: %v", got)
	}
	if got := tagNames(sql2.Tags); !slices.Equal(got, []string{"go"}) || kernel4.Tags == nil || len(kernel4.Tags) != 0 {
		t.Errorf("many_to_many: %v %#v", got, kernel4.Tags)
	}

	// Conditions and order on the relation.
	p, err := db.Query[stRPost](ctx).
		With(postComments.Where(approved.Eq(true)).OrderBy(db.C("body").Asc())).
		Find(int64(1))
	check(t, err)
	if len(p.Comments) != 2 || p.Comments[0].Body != "anon" || p.Comments[1].Body != "nice" {
		t.Errorf("Where/OrderBy: %+v", p.Comments)
	}

	// has_one; self-referencing relations; WithTrashed.
	users, err := db.Query[stRUser](ctx).WithTrashed().OrderBy(id.Asc()).
		With(userProfile, userManager, userReports, userPosts.With(postTags)).
		Get()
	check(t, err)
	ada, grace, linus, alan := users[0], users[1], users[2], users[3]
	if ada.Profile == nil || ada.Profile.Bio != "first programmer" || alan.Profile != nil {
		t.Errorf("has_one: %+v %+v", ada.Profile, alan.Profile)
	}
	if grace.Manager == nil || grace.Manager.Name != "ada" || ada.Manager != nil {
		t.Errorf("self belongs_to: %+v", grace.Manager)
	}
	if got := len(ada.Reports); got != 1 || ada.Reports[0].Name != "grace" {
		t.Errorf("self has_many without trashed: %+v", ada.Reports)
	}
	if len(linus.Posts) != 1 || linus.Posts[0].Title != "kernel" {
		t.Errorf("has_many of a trashed parent: %+v", linus.Posts)
	}
	if got := tagNames(ada.Posts[0].Tags); !slices.Equal(got, []string{"go", "web"}) {
		t.Errorf("nested many_to_many: %v", got)
	}
	u, err := db.Query[stRUser](ctx).With(userReports.WithTrashed()).Find(int64(1))
	check(t, err)
	if len(u.Reports) != 2 {
		t.Errorf("WithTrashed: %+v", u.Reports)
	}

	// The other way round: tags → posts, and Paginate and First load too.
	page, err := db.Query[stRTag](ctx).OrderBy(id.Asc()).With(tagPosts).Paginate(1, 2)
	check(t, err)
	if got := titles(page.Data[0].Posts); !slices.Equal(got, []string{"go", "sql"}) || len(page.Data) != 2 {
		t.Errorf("Paginate: %v", got)
	}
	first, err := db.Query[stRTag](ctx).Where(name.Eq("db")).With(tagPosts).First()
	check(t, err)
	if first.Posts == nil || len(first.Posts) != 0 {
		t.Errorf("First: %#v", first.Posts)
	}

	// Loaded relations encode with the row.
	b, err := json.Marshal(sql2)
	check(t, err)
	if !strings.Contains(string(b), `"user":{"ID":1,"Name":"ada"`) || !strings.Contains(string(b), `"comments":[]`) {
		t.Errorf("JSON: %s", b)
	}

	// Streaming can't batch relations.
	for _, err := range db.Query[stRPost](ctx).With(postUser).All() {
		if err == nil || !strings.Contains(err.Error(), "All can't load relations") {
			t.Errorf("All with With: %v", err)
		}
	}
}

func testRelationsLoad(t *testing.T, ctx context.Context) {
	seedRelations(t, ctx)
	p, err := db.Find[stRPost](ctx, int64(3))
	check(t, err)
	check(t, db.Load(ctx, &p, postUser, postComments.With(commentAuthor)))
	if p.User == nil || p.User.Name != "grace" || len(p.Comments) != 1 || p.Comments[0].Author.Name != "ada" {
		t.Errorf("Load: %+v", p)
	}

	posts, err := db.Query[stRPost](ctx).OrderBy(id.Asc()).Get()
	check(t, err)
	check(t, db.LoadMany(ctx, posts, postTags))
	if got := tagNames(posts[0].Tags); !slices.Equal(got, []string{"go", "web"}) {
		t.Errorf("LoadMany: %v", got)
	}
	check(t, db.LoadMany(ctx, []stRPost{}, postTags)) // nothing to do

	// Loading again replaces what was there.
	posts[0].User = &stRUser{Name: "stale"}
	posts[3].User = &stRUser{Name: "stale"}
	check(t, db.LoadMany(ctx, posts, postUser))
	if posts[0].User.Name != "ada" || posts[3].User != nil {
		t.Errorf("reload: %+v %+v", posts[0].User, posts[3].User)
	}
}

func testRelationsWhereHas(t *testing.T, ctx context.Context) {
	seedRelations(t, ctx)
	postsWhere := func(q *db.Q[stRPost]) []string {
		t.Helper()
		ps, err := q.OrderBy(id.Asc()).Get()
		check(t, err)
		return titles(ps)
	}
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"has_many", postsWhere(db.Query[stRPost](ctx).WhereHas(postComments)), []string{"go", "html"}},
		{"has_many with conds", postsWhere(db.Query[stRPost](ctx).WhereHas(postComments, db.C("body").Eq("tidy"))), []string{"html"}},
		{"relation Where", postsWhere(db.Query[stRPost](ctx).WhereHas(postComments.Where(approved.Eq(false)))), []string{"go", "html"}},
		{"doesn't have", postsWhere(db.Query[stRPost](ctx).WhereDoesntHave(postComments)), []string{"sql", "kernel"}},
		{"many_to_many", postsWhere(db.Query[stRPost](ctx).WhereHas(postTags, name.Eq("web"))), []string{"go", "html"}},
		{"belongs_to skips trashed owners", postsWhere(db.Query[stRPost](ctx).WhereHas(postUser)), []string{"go", "sql", "html"}},
		{"belongs_to WithTrashed", postsWhere(db.Query[stRPost](ctx).WhereHas(postUser.WithTrashed(), name.Eq("linus"))), []string{"kernel"}},
	}
	for _, c := range cases {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, c.got, c.want)
		}
	}
	// Self-referencing: users who manage someone (not trashed).
	managers, err := db.Query[stRUser](ctx).WhereHas(userReports).Get()
	check(t, err)
	if len(managers) != 1 || managers[0].Name != "ada" {
		t.Errorf("self WhereHas: %+v", managers)
	}
	n, err := db.Query[stRUser](ctx).WhereDoesntHave(userProfile).Count()
	check(t, err)
	if n != 1 { // alan (linus is trashed)
		t.Errorf("has_one WhereDoesntHave count = %d", n)
	}
}

func testRelationsPivotWrites(t *testing.T, ctx context.Context) {
	seedRelations(t, ctx)
	post, err := db.Find[stRPost](ctx, int64(2)) // tags: go
	check(t, err)
	tagsOf := func() []string {
		t.Helper()
		check(t, db.Load(ctx, &post, postTags.OrderBy(name.Asc())))
		return tagNames(post.Tags)
	}
	check(t, db.Attach(ctx, &post, postTags, int64(3), int64(1), int64(3))) // go again: skipped
	if got := tagsOf(); !slices.Equal(got, []string{"db", "go"}) {
		t.Errorf("Attach: %v", got)
	}
	check(t, db.Detach(ctx, &post, postTags, int64(1)))
	if got := tagsOf(); !slices.Equal(got, []string{"db"}) {
		t.Errorf("Detach: %v", got)
	}
	check(t, db.Sync(ctx, &post, postTags, int64(1), int64(2)))
	if got := tagsOf(); !slices.Equal(got, []string{"go", "web"}) {
		t.Errorf("Sync: %v", got)
	}
	check(t, db.Detach(ctx, &post, postTags)) // no ids: nothing
	if got := tagsOf(); len(got) != 2 {
		t.Errorf("Detach without ids: %v", got)
	}
	check(t, db.DetachAll(ctx, &post, postTags))
	if got := tagsOf(); len(got) != 0 {
		t.Errorf("DetachAll: %v", got)
	}
	check(t, db.Sync(ctx, &post, postTags)) // nothing to do
	// Other posts' links are untouched.
	other, err := db.Find[stRPost](ctx, int64(1))
	check(t, err)
	check(t, db.Load(ctx, &other, postTags))
	if len(other.Tags) != 2 {
		t.Errorf("post 1 lost its tags: %v", tagNames(other.Tags))
	}
	// Inside a transaction, the writes join it.
	check(t, db.Tx(ctx, func(ctx context.Context) error {
		return db.Attach(ctx, &post, postTags, int64(2))
	}))
	if got := tagsOf(); !slices.Equal(got, []string{"web"}) {
		t.Errorf("Attach in Tx: %v", got)
	}

	// Concurrent Attaches of the same links don't fail: the pivot's
	// primary key and the conflict clause skip what another added.
	if d(ctx).Dialect().Name() != "sqlite" || d(ctx).SQL().Stats().MaxOpenConnections != 1 {
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				p := post
				errs <- db.Attach(ctx, &p, postTags, int64(1), int64(3))
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("concurrent Attach: %v", err)
			}
		}
		if got := tagsOf(); !slices.Equal(got, []string{"db", "go", "web"}) {
			t.Errorf("after concurrent Attach: %v", got)
		}
	}
}

func testRelationsErrors(t *testing.T, ctx context.Context) {
	seedRelations(t, ctx)
	for name, err := range map[string]error{
		"unknown field": db.Load(ctx, &stRPost{ID: 1}, db.RelOf[stRPost, stRTag]("Labels")),
		"wrong type":    db.Load(ctx, &stRPost{ID: 1}, db.RelOf[stRPost, stRUser]("Tags")),
		"not m2m":       db.Attach(ctx, &stRPost{ID: 1}, postComments, 1),
		"no key yet":    db.Attach(ctx, &stRPost{}, postTags, 1),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	_, err := db.Query[stRPost](ctx).WhereHas(db.RelOf[stRPost, stRTag]("Nope")).Get()
	if err == nil {
		t.Error("WhereHas with a bad relation: no error")
	}
	_, err = db.Query[stRPost](ctx).WhereHas(postComments.With(commentAuthor)).Get()
	if err == nil {
		t.Error("WhereHas with nested relations: no error")
	}
	_, err = db.Query[stRPost](ctx).With(postUser, postUser).Get()
	if err == nil || !strings.Contains(err.Error(), "loaded twice") {
		t.Errorf("a relation twice: %v", err)
	}
	// A pivot column that doesn't exist is an error on every database
	// (SQLite would read an unknown quoted name as a string).
	badPivot := db.RelOf[stRBadPivot, stRTag]("Tags")
	if err := badPivot.Err(); err != nil {
		t.Fatal(err) // keys of a pivot aren't checked before use
	}
	rows := []stRBadPivot{{ID: 1}}
	if err := db.LoadMany(ctx, rows, badPivot); err == nil {
		t.Error("LoadMany with a missing pivot column: no error")
	}
	if err := db.Attach(ctx, &rows[0], badPivot, int64(1)); err == nil {
		t.Error("Attach with a missing pivot column: no error")
	}
	if err := db.DetachAll(ctx, &rows[0], badPivot); err == nil {
		t.Error("DetachAll with a missing pivot column: no error")
	}
	// Err resolves keys.
	if err := db.RelOf[stRBadFK, stRPost]("Posts").Err(); err == nil || !strings.Contains(err.Error(), "no column") {
		t.Errorf("Err with a missing fk: %v", err)
	}
}

// stRBadPivot names a pivot column that doesn't exist.
type stRBadPivot struct {
	ID   int64    `db:"id,pk"`
	Tags []stRTag `rel:"many_to_many,pivot=st_r_post_tags,fk=usr_id,related_fk=tag_id"`
}

// TableName names the table.
func (stRBadPivot) TableName() string { return "st_r_posts" }

// stRBadFK's posts have no st_r_bad_fk_id column.
type stRBadFK struct {
	ID    int64     `db:"id,pk"`
	Posts []stRPost `rel:"has_many"`
}

// TableName names the table.
func (stRBadFK) TableName() string { return "st_r_users" }

// testRelationsLarge loads more related rows than one IN (…) list holds,
// in order.
func testRelationsLarge(t *testing.T, ctx context.Context) {
	seedRelations(t, ctx)
	tags := make([]stRTag, 1100)
	ids := make([]any, len(tags))
	for i := range tags {
		tags[i] = stRTag{Name: fmt.Sprintf("t%04d", i)}
		check(t, db.Create(ctx, &tags[i]))
		ids[i] = tags[i].ID
	}
	post := stRPost{ID: 4}
	check(t, db.Attach(ctx, &post, postTags, ids...))
	check(t, db.Load(ctx, &post, postTags.OrderBy(name.Desc())))
	got := tagNames(post.Tags)
	if len(got) != 1100 || !slices.IsSortedFunc(got, func(a, b string) int { return strings.Compare(b, a) }) {
		t.Errorf("%d tags, sorted descending: %v", len(got), slices.IsSortedFunc(got, func(a, b string) int { return strings.Compare(b, a) }))
	}
	// And more parents than one list: every tag's posts.
	all, err := db.Query[stRTag](ctx).With(tagPosts).Get()
	check(t, err)
	n := 0
	for _, tag := range all {
		n += len(tag.Posts)
	}
	if len(all) != 1103 || n != 1104 { // 4 seeded links + 1100
		t.Errorf("%d tags, %d links", len(all), n)
	}
}
