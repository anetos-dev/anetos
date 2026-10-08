---
title: Validation
since: v0.1.0
group: "Basics"
weight: 105
---

# Validation

Declare rules on an input struct, get one clear message per field, and add
checks of your own.

## Before you start

Read [Handlers and requests](handlers.md): rules live on the same input
structs that `web.H` binds.

## Steps

### 1. Add rules to the input

Write rules in a `validate` tag, Laravel style: rules separated by `|`,
parameters after `:` and separated by commas.

```go
// SignUp is the input for POST /signup.
type SignUp struct {
	Name     string `json:"name" validate:"required|max:100"`
	Email    string `json:"email" validate:"required|email"`
	Username string `json:"username" validate:"required|between:3,20|slug"`
	Password string `json:"password" validate:"required|min:12|confirmed"`
	// PasswordConfirmation is compared by the "confirmed" rule on Password.
	PasswordConfirmation string `json:"password_confirmation"`

	Plan    string   `json:"plan" validate:"required|in:free,pro,team"`
	Company string   `json:"company" validate:"required_if:plan,team"`
	Seats   *int     `json:"seats" validate:"min:1|max:500"`
	Website string   `json:"website" label:"web site" validate:"url:https"`
	Tags    []string `json:"tags" validate:"max:5|distinct"`
	Terms   bool     `json:"terms" validate:"accepted"`
}
```

(Copied from [`examples/validation`](../../../examples/validation/main.go), region `input`.)

`web.H` checks the rules after binding and before your handler. If any fail,
the handler doesn't run and the client gets a **422**:

```json
{
  "type": "about:blank",
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "The given data was invalid.",
  "errors": {
    "email": "The email field must be a valid email address.",
    "password": "Use at least 12 characters for your password."
  }
}
```

A browser posting an HTML form gets no 422: on routes with sessions, it
is redirected back to the form, and the page shows the messages and the
submitted values ([Handle HTML forms](forms.md)).

Rules run in order and stop at the first failure, so each field gets one
message. Every rule is listed in the
[rules reference](../reference/validation-rules.md).

### 2. Decide what is optional

Only the `required` family and `accepted` run on an empty field. Every
other rule skips it, so `validate:"url:https"` means "optional, but an HTTPS
URL if given", and `max:5` on an empty list passes (add `required` to
demand at least one item).

"Empty" means a nil pointer, a blank string, an empty slice or map, or a
zero struct or array value, such as a zero `time.Time` or UUID. Numbers and
booleans are **never** empty, because zero may be a real answer. To make a
number optional, use a pointer: `Seats *int` with `min:1|max:500` accepts a
missing value but rejects `0`.

`required` also rejects `0` and `false` in non-pointer fields. For a
checkbox that must be ticked, use `accepted`.

### 3. Name fields in messages

Messages use the field's key (its `json` name, else its `form`, `query`,
`path` or `header` name), turned into words: `first_name` becomes "first
name". Set a `label` tag to choose the words, as `Website` does above ("The
web site field must be a valid URL."). When an HTML form posts the input,
errors are keyed by the `form` name if it differs from the `json` name, so
each message matches its input.

Nested structs, slices and maps of structs are validated too. Their errors
use dotted keys: `address.city`, `items.0.name`. Embedded structs are
flattened the way `encoding/json` flattens them.

### 4. Change messages

Add a `ValidationMessages` method to the input. Keys are `field.rule` for
one field or `rule` for the whole struct:

```go
// ValidationMessages replaces default messages for SignUp. Keys are
// "field.rule" or just "rule".
func (SignUp) ValidationMessages() map[string]string {
	return map[string]string{
		"terms.accepted": "Please accept the terms to continue.",
		"password.min":   "Use at least {0} characters for your password.",
	}
}
```

(Region `messages`.)

Messages can use `{label}`, the rule's parameters `{0}`, `{1}`, … and
`{list}` (all parameters, comma-separated). A key that matches no rule is
a startup error, so typos don't go unnoticed.

Messages are in the request's language. The defaults, the labels and a
`ValidationMessages` value that is a catalog key come from the
translation catalogs (`validation.required`,
`validation.attributes.email`): change them for every struct there, and
translate them, as [Translations](translations.md#6-translate-the-frameworks-messages)
shows.

### 5. Add a custom rule

Register named rules in an `init` function, so they exist before routes are
added:

```go
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func init() {
	validate.Register("slug", "The {label} field may only contain lowercase letters, numbers and single dashes.",
		func(ctx context.Context, f validate.Field) (bool, error) {
			s, _ := f.Value.(string)
			return slugPattern.MatchString(s), nil
		})
}
```

(Region `custom-rule`.)

The rule receives the field's key, label, value (pointers already followed),
the tag parameters and the parent struct. Return an error only when the
check itself failed (a lookup service was down); that becomes a 500, not a
validation message.

### 6. Check what tags can't

Some checks need a database or several fields at once. Do them in the
handler, or in a `Validate(ctx) error` method on the input, which runs after
the tag rules pass. Report a problem with `validate.Fail` (or several with a
`validate.Errors`) so the client gets the same 422 shape:

```go
// Users is a stand-in for a user repository.
type Users interface {
	UsernameTaken(ctx context.Context, username string) (bool, error)
}

// SignedUp is the answer to a sign-up.
type SignedUp struct {
	Username string `json:"username"`
}

// signUpHandler checks what tags can't: whether the username is free.
func signUpHandler(users Users) func(c *web.Ctx, in SignUp) (SignedUp, error) {
	return func(c *web.Ctx, in SignUp) (SignedUp, error) {
		taken, err := users.UsernameTaken(c, in.Username)
		if err != nil {
			return SignedUp{}, err // a 500: the check itself failed
		}
		if taken {
			return SignedUp{}, validate.Fail("username", "This username is already taken.") // a 422, like the tag rules
		}
		return SignedUp{Username: in.Username}, nil // 201: the route's Status
	}
}
```

(Region `handler-check`.)

Any other error is a **500** and its text stays out of the response, so a
failed database call never leaks connection details to clients.

### 7. Validate outside HTTP

`validate.Struct` works on any struct, for example a job payload or a CLI
command's input. It returns nil, a `*validate.Errors`, or the error of a
custom rule that couldn't run:

```go
in := SignUp{
	Name:     "Sam",
	Email:    "sam@example",
	Username: "Sam_H",
	Password: "short",
	Plan:     "team",
}
err := validate.Struct(context.Background(), &in)
if errs, ok := errors.AsType[*validate.Errors](err); ok {
	for _, key := range errs.Keys() {
		fmt.Printf("%-10s %s\n", key, errs.Get(key))
	}
}
```

(Region `standalone`.)

## How it works

The first time a struct type is validated (for `web.H`, when the route is
added), its tags are parsed into a plan that is cached for the life of the
process. Tag mistakes (an unknown rule, `min:abc`, `email` on an `int`,
`required_if` naming a missing field, a message key with a typo) are
reported then, so `web.H` panics at startup rather than on the first
request.

Per request only the plan runs. With the built-in rules, valid input is
checked without allocating memory, except for maps of structs and file
rules (which open the upload); messages are built only when a rule fails.

> **Coming from Laravel?** Rule names and messages follow Laravel's, and the
> input struct is your Form Request. Differences: rules live on the struct,
> not in a `rules()` array; `nullable`, `bail` and `sometimes` aren't needed
> (the startup error says why); `min`/`max` on uploads count files, and
> file size uses `max_size:2MB`; `unique` and `exists` come from the db
> package (see the [rules reference](../reference/validation-rules.md#database-rules)).

## Testing it

```go
// illustrative
func TestSignUpRules(t *testing.T) {
	err := validate.Struct(context.Background(), &SignUp{Email: "nope"})
	errs, ok := errors.AsType[*validate.Errors](err)
	if !ok || !errs.Has("email") {
		t.Fatalf("got %v, want an email error", err)
	}
}
```

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| Panic at startup: `unknown rule "emial"` | A typo, or a custom rule registered after the route was added | Check the [rules reference](../reference/validation-rules.md); call `validate.Register` in `init` |
| Panic at startup: `malformed rule "required,email"` | go-playground style tags, possibly on a type from another library | Separate rules with `\|` and parameters with `:`: `required\|max:10`. For a third-party type, add `validate:"-"` to the field that holds it |
| Panic at startup: `rule "email" does not apply to int fields` | The rule doesn't fit the field's type | Use a rule for that type, or change the type |
| `min:1` never fails on an optional number or list | The field wasn't sent or the list is empty, so only `required` runs | Add `required`, or keep it optional on purpose |
| `required` fails for `0` or `false` | Non-pointer numbers and booleans treat zero as missing | Use `*int` / `*bool`, or `accepted` for checkboxes |
| `required` fails for a zero amount of a decimal type | Struct values count as empty when zero | Use a pointer (`*decimal.Decimal`) |
| A `Validate` method's error comes back as a 500 | Plain errors are treated as failures, not messages | Return `validate.Fail(field, message)` |

## Next steps

- [Handle HTML forms](forms.md)
- [Validation rules reference](../reference/validation-rules.md)
- [Handlers and requests](handlers.md)
- [Store files](storage.md): store the uploads you validated
