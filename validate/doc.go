// SPDX-License-Identifier: Apache-2.0

// Package validate checks structs against rules written in `validate` struct
// tags and reports one message per field.
//
//	type Register struct {
//		Name     string `json:"name"     validate:"required|max:100"`
//		Email    string `json:"email"    validate:"required|email"`
//		Password string `json:"password" validate:"required|min:12|confirmed"`
//		PasswordConfirmation string `json:"password_confirmation"`
//		Role     string `json:"role"     validate:"in:reader,editor"`
//		Terms    bool   `json:"terms"    validate:"accepted"`
//	}
//
//	err := validate.Struct(ctx, &in) // nil, *validate.Errors or a rule's error
//
// Handlers built with web.H validate their input automatically after
// binding, and a failure becomes a 422 response listing the messages.
//
// # Tags
//
// The syntax is Laravel's: rules are separated by "|", parameters follow
// ":" and are separated by commas: `validate:"required|between:3,20|in:a,b,c"`.
// Rules run in order and stop at the first failure, so each field gets at
// most one message. `validate:"-"` skips a field and its nested fields.
//
// Rules other than the "required" family and "accepted" skip empty fields, so optional
// fields only need rules for their format. Nil pointers, blank strings,
// empty slices and maps, and zero structs (such as a zero time.Time) are
// empty. Numbers and booleans are
// never empty, since a zero may be deliberate; use a pointer (*int, *bool)
// to tell "not sent" from zero.
//
// # Keys and labels
//
// Errors are keyed by the name clients use: the json tag, else the form,
// query, path or header tag, else the Go field name. Nested structs, slices
// and maps of structs are validated too, with keys like "address.city" and
// "items.0.name". Messages use a label derived from the key ("first_name"
// becomes "first name"); set a `label` tag to change it.
//
// # Messages
//
// Messages are in the language of the context (package i18n): the
// catalogs' validation.<rule> messages, English by default, in the style
// of "The email field must be a valid email address." Labels come from
// validation.attributes.<key> when a catalog has it. A struct can replace
// messages with a ValidationMessages method returning templates (or
// catalog keys, which are translated) keyed by "key.rule" or "rule":
//
//	func (Register) ValidationMessages() map[string]string {
//		return map[string]string{
//			"email.required": "We need your email to send the receipt.",
//			"min":            "{label} is too short (at least {0}).",
//		}
//	}
//
// Templates may use {label}, the rule's parameters {0}, {1}, … and {list}
// (all parameters joined with ", "). A parameter the catalog has under
// validation.values.<parameter> is translated ("now").
//
// # Custom rules
//
// [Register] adds a named rule, usually from an init function:
//
//	validate.Register("slug", "The {label} field must be a slug.",
//		func(ctx context.Context, f validate.Field) (bool, error) {
//			s, _ := f.Value.(string)
//			return slugRE.MatchString(s), nil
//		})
//
// Checks that need a database or several fields belong in a Validate(ctx)
// method on the input type (see web.Validator), which runs after the tag
// rules pass, or in the handler. Report problems with [Fail] or an [Errors]
// built by hand.
//
// # Performance
//
// A struct type's tags are parsed once into a [Plan] and cached; tag
// mistakes (unknown rules, bad parameters, rules on the wrong field type,
// references to missing fields) are reported then, which for web.H means
// at startup. Validating walks the plan with reflection but never parses
// tags; with the built-in rules, a valid struct is checked without
// allocating (maps of structs and file rules excepted).
//
// Fields are resolved like encoding/json resolves them: embedded structs
// are flattened, shadowed fields are ignored, and a field inside a nil
// embedded pointer counts as empty.
package validate
