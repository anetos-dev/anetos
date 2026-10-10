// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"anetos.dev/anetos/validate"
)

// Tool is something the model can call: a definition it reads, and a
// call that runs in the app. [NewTool] makes one from a Go function.
type Tool interface {
	// Definition describes the tool to the model.
	Definition() ToolSpec
	// Call runs the tool with the model's input, a JSON object, and
	// returns its output for the model (JSON or text). ctx is the
	// caller's, so the current user and their permissions apply.
	//
	// An error with a 4xx status ([web.StatusCoder]: invalid input,
	// not allowed, not found) is reported to the model, which can
	// correct its input or tell the user; any other error stops the
	// generation, which returns it.
	Call(ctx context.Context, input json.RawMessage) (string, error)
}

// ToolSpec is a tool's definition, as the model sees it.
type ToolSpec struct {
	// Name is the tool's name: letters, digits, _ and -, at most 64.
	Name string
	// Description tells the model what the tool does and when to use it.
	Description string
	// Input is the schema of the tool's input, a JSON object.
	Input *Schema
}

var toolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// funcTool is a Tool made by Func.
type funcTool[In, Out any] struct {
	spec ToolSpec
	plan *validate.Plan
	fn   func(context.Context, In) (Out, error)
}

// NewTool returns a tool that runs fn. The model's input is decoded into
// In, a struct whose schema the model is given ([SchemaFor]: json
// names, description tags, validate rules), and checked with In's
// validate tags before fn runs; invalid input goes back to the model to
// correct. fn's output is sent to the model as JSON (a value of a
// string type as it is).
//
//	type FindOrder struct {
//		Number int `json:"number" description:"The order's number" validate:"required|min:1"`
//	}
//
//	var findOrder = ai.NewTool("find_order", "Look up one of the customer's orders by its number",
//		func(ctx context.Context, in FindOrder) (Order, error) {
//			user, err := auth.Current[*User](ctx)
//			if err != nil {
//				return Order{}, err
//			}
//			return ordersOf(ctx, user).Where(OrderCols.Number.Eq(in.Number)).First() // db.ErrNotFound: 404
//		})
//
// fn runs with the context of the call that started the generation, so
// package auth and policies see the current user: a tool can do no more
// than the user could. Return a 4xx error (auth.ErrForbidden, db.ErrNotFound,
// web.Error) to tell the model why it can't; it's reported to the model
// as a web client would see it. Any other error stops the generation.
//
// NewTool panics if name isn't a valid tool name, In isn't a struct, or
// In's tags are wrong: tools are made at startup, and these are
// programming errors. A panic in fn isn't recovered: it ends the call,
// as it would end a handler.
func NewTool[In, Out any](name, description string, fn func(ctx context.Context, in In) (Out, error)) Tool {
	if !toolName.MatchString(name) {
		panic(fmt.Sprintf("ai: tool name %q: use 1 to 64 letters, digits, _ and -", name))
	}
	if fn == nil {
		panic(fmt.Sprintf("ai: tool %s: nil function", name))
	}
	s, err := SchemaFor[In]()
	if err != nil {
		panic(fmt.Sprintf("ai: tool %s: %v", name, err))
	}
	plan, err := validate.Compile(reflect.TypeFor[In]())
	if err != nil {
		panic(fmt.Sprintf("ai: tool %s: %v", name, err))
	}
	return &funcTool[In, Out]{spec: ToolSpec{Name: name, Description: description, Input: s}, plan: plan, fn: fn}
}

// Func is [NewTool].
//
// Deprecated: Use NewTool; Func is removed in v0.6.
//
//go:fix inline
func Func[In, Out any](name, description string, fn func(ctx context.Context, in In) (Out, error)) Tool {
	return NewTool(name, description, fn)
}

func (t *funcTool[In, Out]) Definition() ToolSpec { return t.spec }

func (t *funcTool[In, Out]) Call(ctx context.Context, input json.RawMessage) (string, error) {
	var in In
	if raw := bytes.TrimSpace(input); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", &InputError{Err: err, detail: jsonProblem(err)}
		}
	}
	if err := t.plan.Validate(ctx, &in); err != nil {
		if errs, ok := errors.AsType[*validate.Errors](err); ok {
			return "", &InputError{Err: errs}
		}
		return "", err // a rule that couldn't run
	}
	out, err := t.fn(ctx, in)
	if err != nil {
		return "", err
	}
	return encodeOutput(out)
}

// encodeOutput is a tool's output as the model gets it: a string (of
// any string type without its own encoding) as it is, anything else as
// JSON.
func encodeOutput(out any) (string, error) {
	if v := reflect.ValueOf(out); v.Kind() == reflect.String &&
		!implements(v.Type(), jsonMarshalerType) && !implements(v.Type(), textMarshalerType) {
		return v.String(), nil
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encode the output: %w", err)
	}
	return string(data), nil
}

// InputError is a tool input that doesn't fit the tool: not its
// schema's JSON (Err is encoding/json's error), or failing its validate
// rules (Err is then a *validate.Errors). It is reported to the model,
// to correct: the fields' messages, or which field has the wrong JSON
// type.
type InputError struct {
	// Err says what's wrong.
	Err    error
	detail string // what the model is told, for a JSON error
}

// jsonProblem describes a decoding error for the model, without Go's
// type and field names.
func jsonProblem(err error) string {
	if te, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		if te.Field == "" {
			return "the input must be " + jsonKind(te.Type)
		}
		return te.Field + " must be " + jsonKind(te.Type)
	}
	if _, ok := errors.AsType[*json.SyntaxError](err); ok {
		return "the input isn't valid JSON"
	}
	return "the input doesn't match the tool's schema"
}

// jsonKind names t's JSON type, as its schema says it ("a string").
func jsonKind(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == timeType:
		return "a date-time string"
	case t == numberType:
		return "a number"
	case implements(t, textMarshalerType):
		return "a string"
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return "a base64 string"
		}
		return "a list"
	case reflect.Array:
		return "a list"
	case reflect.Struct, reflect.Map:
		return "an object"
	}
	return "a value of its schema's type"
}

// Error says what's wrong with the input.
func (e *InputError) Error() string { return "ai: invalid tool input: " + e.Err.Error() }

// Unwrap returns Err.
func (e *InputError) Unwrap() error { return e.Err }

// HTTPStatus is 422: the input is the model's mistake.
func (e *InputError) HTTPStatus() int { return http.StatusUnprocessableEntity }

// statusCoder is web.StatusCoder.
type statusCoder interface{ HTTPStatus() int }

// clientError is *web.HTTPError: its client-safe message and fields.
type clientError interface {
	ClientMessage() string
	ClientFields() map[string]string
}

// fieldErrorer is web.FieldErrorer.
type fieldErrorer interface{ FieldErrors() map[string]string }

// modelError returns what the model is told of a tool's error, and
// whether it is told at all: only errors with a 4xx status, and only
// what a web client would see: the status text; the message and fields
// of the error that has the status, if it's a web.HTTPError, or its
// field messages; else the field messages of a validation error in the
// chain. Never an internal cause.
func modelError(err error) (string, bool) {
	var sc statusCoder
	if !errors.As(err, &sc) {
		return "", false
	}
	status := sc.HTTPStatus()
	if status < 400 || status > 499 {
		return "", false
	}
	var b strings.Builder
	b.WriteString(http.StatusText(status))
	var fields map[string]string
	switch e := sc.(type) {
	case *InputError:
		if errs, ok := errors.AsType[*validate.Errors](e.Err); ok {
			fields = errs.FieldErrors()
		} else if e.detail != "" {
			b.WriteString(": " + e.detail)
		}
	case clientError:
		if m := e.ClientMessage(); m != "" && m != http.StatusText(status) {
			b.WriteString(": " + m)
		}
		fields = e.ClientFields()
	case fieldErrorer:
		fields = e.FieldErrors()
	}
	if _, isInput := sc.(*InputError); fields == nil && !isInput {
		var fe fieldErrorer
		if errors.As(err, &fe) {
			fields = fe.FieldErrors() // as the web layer shows them
		}
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		b.WriteString("\n- " + k + ": " + fields[k])
	}
	return b.String(), true
}
