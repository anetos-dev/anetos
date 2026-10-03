// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"
)

// Price is a model's price per million tokens, in the app's currency.
type Price struct {
	// Input is the price of input tokens.
	Input float64
	// Output is the price of output tokens.
	Output float64
	// CacheRead is the price of input tokens read from the provider's
	// prompt cache; 0 charges them at Input.
	CacheRead float64
	// CacheWrite is the price of input tokens written to it; 0 charges
	// them at Input.
	CacheWrite float64
}

// Cost returns what u costs at p.
func (p Price) Cost(u Usage) float64 {
	read, write := p.CacheRead, p.CacheWrite
	if read == 0 {
		read = p.Input
	}
	if write == 0 {
		write = p.Input
	}
	plain := max(u.InputTokens-u.CacheReadTokens-u.CacheWriteTokens, 0)
	return (float64(plain)*p.Input + float64(u.CacheReadTokens)*read +
		float64(u.CacheWriteTokens)*write + float64(u.OutputTokens)*p.Output) / 1e6
}

// Budget limits what a user's calls may use in a period. Usage is counted
// on the rate limiter (package web/ratelimit, in the app's cache), in
// periods aligned to the clock: a budget per day starts again at midnight
// UTC.
type Budget struct {
	// Tokens bounds the input and output tokens a period; 0 for no limit.
	Tokens int
	// Cost bounds the cost a period, at [UsageConfig.Prices]; 0 for no
	// limit. Models without a price cost nothing.
	Cost float64
	// Per is the period: 24 * time.Hour for a day.
	Per time.Duration
}

func (b Budget) limited() bool { return (b.Tokens > 0 || b.Cost > 0) && b.Per > 0 }

// BudgetError is the error of a call refused because its user's [Budget]
// is spent. Calls return it wrapped in a *web.HTTPError with status 429
// and a message for the user, so a handler can return it as it is.
type BudgetError struct {
	// UserID is the user whose budget is spent.
	UserID string
	// RetryAfter is how long until the budget's period starts again.
	RetryAfter time.Duration
}

// Error says whose budget is spent.
func (e *BudgetError) Error() string {
	return fmt.Sprintf("ai: user %s's budget is spent until the period starts again, in %s", e.UserID, e.RetryAfter.Round(time.Second))
}

// UsageConfig turns on usage tracking ([Client.TrackUsage]).
type UsageConfig struct {
	// Prices are the models' prices, by name: the name the provider
	// reports in responses ("claude-sonnet-4-5-20250929"), else the one
	// requested. Usage of a model without a price costs nothing.
	Prices map[string]Price
	// Budget returns a user's budget (none, its zero value, for no limit):
	// one for everyone, or by the user's plan. It runs once per call, for
	// calls with a user ([ForUser], or the signed-in user).
	Budget func(ctx context.Context, userID string) (Budget, error)
}

// TrackUsage records the usage and cost of every response in the
// ai_usage table ([Migrations]), for the user it's for ([ForUser], or the
// signed-in user), and enforces their [Budget]: a call whose user has
// spent theirs fails with a [*BudgetError] (429) before its next request
// to the model. A response can go past the budget: what it will use isn't
// known beforehand. Budgets need the app's cache (cache.ForApp). Call it
// at startup, before the client's first call.
func (cl *Client) TrackUsage(cfg UsageConfig) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.usage = &cfg
}

func (cl *Client) usageConfig() *UsageConfig {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	return cl.usage
}

// ForUser makes the call for the user with userID (an AuthID): its usage
// is recorded for them and counts against their budget
// ([Client.TrackUsage]). By default, a call is for the signed-in user, if
// any: set it in queue jobs and other work done for a user outside their
// requests.
func ForUser(userID string) Option {
	return optionFunc(func(c *call) { c.user, c.userSet = userID, true })
}

// UsageRecord is a row of ai_usage: one model response's usage.
type UsageRecord struct {
	// ID is the primary key.
	ID int64 `db:"id,pk" json:"id"`
	db.Timestamps
	// UserID is the user the call was for ("" for none).
	UserID string `db:"user_id" json:"user_id"`
	// ConversationID is the stored conversation's, if any.
	ConversationID *int64 `db:"conversation_id" json:"conversation_id"`
	// Agent is the agent's name, if any.
	Agent string `db:"agent" json:"agent"`
	// Provider is the provider's name ("anthropic").
	Provider string `db:"provider" json:"provider"`
	// Model is the model that answered, as the provider names it.
	Model string `db:"model" json:"model"`
	// InputTokens counts the response's input tokens ([Usage]).
	InputTokens int64 `db:"input_tokens" json:"input_tokens"`
	// OutputTokens counts its output tokens.
	OutputTokens int64 `db:"output_tokens" json:"output_tokens"`
	// CacheReadTokens is the part of InputTokens read from the cache.
	CacheReadTokens int64 `db:"cache_read_tokens" json:"cache_read_tokens"`
	// CacheWriteTokens is the part of InputTokens written to it.
	CacheWriteTokens int64 `db:"cache_write_tokens" json:"cache_write_tokens"`
	// Cost is its cost at the model's price (0 without one).
	Cost float64 `db:"cost" json:"cost"`
	// Estimated is true for a request cut off partway (a stream's reader
	// left, a timeout, a cancellation): its tokens are guessed from the
	// request's size and the output yielded, about four bytes a token.
	Estimated bool `db:"estimated" json:"estimated"`
}

// TableName implements db.Tabler.
func (UsageRecord) TableName() string { return "ai_usage" }

// UsageTotal is the usage of many responses ([TotalUsage]).
type UsageTotal struct {
	Usage
	// Cost is their total cost.
	Cost float64
	// Responses counts them.
	Responses int64
}

// TotalUsage returns the usage recorded for the user since a time: what
// they used today, say.
func TotalUsage(ctx context.Context, userID string, since time.Time) (UsageTotal, error) {
	type row struct {
		Responses        int64           `db:"responses"`
		InputTokens      sql.NullInt64   `db:"input_tokens"`
		OutputTokens     sql.NullInt64   `db:"output_tokens"`
		CacheReadTokens  sql.NullInt64   `db:"cache_read_tokens"`
		CacheWriteTokens sql.NullInt64   `db:"cache_write_tokens"`
		Cost             sql.NullFloat64 `db:"cost"`
	}
	rows, err := db.Select[row](db.Query[UsageRecord](ctx).Where(colUserID.Eq(userID), db.Col[time.Time]("created_at").Gte(since.UTC())),
		"COUNT(*) AS responses", "SUM(input_tokens) AS input_tokens", "SUM(output_tokens) AS output_tokens",
		"SUM(cache_read_tokens) AS cache_read_tokens", "SUM(cache_write_tokens) AS cache_write_tokens", "SUM(cost) AS cost")
	if err != nil || len(rows) == 0 {
		return UsageTotal{}, err
	}
	r := rows[0]
	return UsageTotal{
		Usage: Usage{InputTokens: r.InputTokens.Int64, OutputTokens: r.OutputTokens.Int64,
			CacheReadTokens: r.CacheReadTokens.Int64, CacheWriteTokens: r.CacheWriteTokens.Int64},
		Cost: r.Cost.Float64, Responses: r.Responses,
	}, nil
}

// tracking is a call's usage tracking: its user and budget.
type tracking struct {
	cfg    *UsageConfig
	user   string
	budget Budget
}

// startTracking resolves the call's user and budget, if the client
// tracks usage.
func (cl *Client) startTracking(ctx context.Context, c *call) (*tracking, error) {
	cfg := cl.usageConfig()
	if cfg == nil {
		return nil, nil
	}
	t := &tracking{cfg: cfg, user: c.user}
	if !c.userSet {
		id, err := auth.CurrentID(ctx)
		switch {
		case err == nil:
			t.user = id
		case !errors.Is(err, auth.ErrUnauthenticated):
			return nil, err
		}
	}
	if t.user != "" && cfg.Budget != nil {
		b, err := cfg.Budget(ctx, t.user)
		if err != nil {
			return nil, fmt.Errorf("ai: the budget of user %s: %w", t.user, err)
		}
		t.budget = b
	}
	return t, nil
}

func (t *tracking) tokenLimit() ratelimit.Limit {
	return ratelimit.Limit{Max: t.budget.Tokens, Window: t.budget.Per}
}

func (t *tracking) costLimit() ratelimit.Limit {
	return ratelimit.Limit{Max: int(math.Round(t.budget.Cost * 1e6)), Window: t.budget.Per}
}

// check refuses a request when the user's budget is spent.
func (t *tracking) check(ctx context.Context) error {
	if t == nil || t.user == "" || !t.budget.limited() {
		return nil
	}
	var spent ratelimit.Result
	for _, l := range []struct {
		on    bool
		key   string
		limit ratelimit.Limit
	}{
		{t.budget.Tokens > 0, "ai:tokens:" + t.user, t.tokenLimit()},
		{t.budget.Cost > 0, "ai:cost:" + t.user, t.costLimit()},
	} {
		if !l.on {
			continue
		}
		res, err := ratelimit.Check(ctx, l.key, l.limit)
		if err != nil {
			return fmt.Errorf("ai: checking user %s's budget: %w", t.user, err)
		}
		if !res.Allowed && res.RetryAfter() > spent.RetryAfter() {
			spent = res
		}
	}
	if spent.Limit == 0 {
		return nil
	}
	wait := spent.RetryAfter()
	return &web.HTTPError{
		Status:  http.StatusTooManyRequests,
		Message: i18n.T(context.Background(), "ai.usage_limit", "wait", humanDuration(wait)), // English, for logs
		Key:     "ai.usage_limit",
		Args:    []any{"wait", humanDuration(wait)},
		Err:     &BudgetError{UserID: t.user, RetryAfter: wait},
	}
}

// record counts a response against the budget and stores its record
// (estimated: a stream that stopped partway). Failures are logged: the
// answer is there all the same.
func (cl *Client) record(ctx context.Context, t *tracking, c *call, provider string, req *Request, resp *Response, estimated bool) {
	if t == nil {
		return
	}
	// The tokens are spent whatever happens to the call: count them even
	// if the client has gone. (In a transaction that rolls back, the
	// record goes with it: don't call models inside transactions.)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	cost := 0.0
	if p, ok := t.cfg.Prices[resp.Model]; ok {
		cost = p.Cost(resp.Usage)
	} else if p, ok := t.cfg.Prices[req.Model]; ok {
		cost = p.Cost(resp.Usage)
	}
	if t.user != "" && t.budget.limited() {
		var err error
		if t.budget.Tokens > 0 {
			_, err = ratelimit.AllowN(ctx, "ai:tokens:"+t.user, int(resp.Usage.InputTokens+resp.Usage.OutputTokens), t.tokenLimit())
		}
		if err == nil && t.budget.Cost > 0 {
			_, err = ratelimit.AllowN(ctx, "ai:cost:"+t.user, int(math.Round(cost*1e6)), t.costLimit())
		}
		if err != nil {
			cl.logger().WarnContext(ctx, "ai: counting usage against the budget failed", "user", t.user, "error", err)
		}
	}
	model := resp.Model
	if model == "" {
		model = req.Model
	}
	rec := UsageRecord{
		UserID: t.user, ConversationID: c.conversation, Agent: c.name, Provider: provider, Model: model,
		InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens,
		CacheReadTokens: resp.Usage.CacheReadTokens, CacheWriteTokens: resp.Usage.CacheWriteTokens, Cost: cost,
		Estimated: estimated,
	}
	if err := db.Create(ctx, &rec); err != nil {
		cl.logger().ErrorContext(ctx, "ai: recording usage failed", "user", t.user, "error", err)
	}
}

// humanDuration says d for people: "3 hours", "12 minutes", "40 seconds".
func humanDuration(d time.Duration) string {
	unit := func(n int64, name string) string {
		if n == 1 {
			return "1 " + name
		}
		return fmt.Sprintf("%d %ss", n, name)
	}
	switch {
	case d >= 2*time.Hour:
		return unit(int64((d+time.Hour/2)/time.Hour), "hour")
	case d >= 2*time.Minute:
		return unit(int64((d+time.Minute/2)/time.Minute), "minute")
	default:
		return unit(max(int64((d+time.Second/2)/time.Second), 1), "second")
	}
}

// estimateUsage guesses the usage of a stream that stopped or failed
// partway, at about four bytes a token: its whole input, and the output
// yielded so far.
func estimateUsage(req *Request, outBytes int) Usage {
	in := len(req.System)
	if b, err := json.Marshal(req.Messages); err == nil {
		in += len(b)
	}
	if b, err := json.Marshal(req.Tools); err == nil {
		in += len(b)
	}
	return Usage{InputTokens: int64((in + 3) / 4), OutputTokens: int64((outBytes + 3) / 4)}
}
