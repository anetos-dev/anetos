// SPDX-License-Identifier: Apache-2.0

package validate

// messages are the default English messages. Keys are rule names, or
// "rule.kind" for size rules, whose wording depends on the field's kind
// (string, numeric or array). {label} is the field's label, {0}, {1}, … the
// rule's arguments and {list} all arguments joined with ", ".
var messages = map[string]string{
	// Presence.
	"required":         "The {label} field is required.",
	"required_if":      "The {label} field is required when {0} is {1}.",
	"required_unless":  "The {label} field is required unless {0} is {1}.",
	"required_with":    "The {label} field is required when {list} is present.",
	"required_without": "The {label} field is required when {list} is not present.",
	"accepted":         "The {label} field must be accepted.",

	// Size.
	"min.string":      "The {label} field must be at least {0} characters.",
	"min.numeric":     "The {label} field must be at least {0}.",
	"min.array":       "The {label} field must have at least {0} items.",
	"max.string":      "The {label} field must not be greater than {0} characters.",
	"max.numeric":     "The {label} field must not be greater than {0}.",
	"max.array":       "The {label} field must not have more than {0} items.",
	"size.string":     "The {label} field must be {0} characters.",
	"size.numeric":    "The {label} field must be {0}.",
	"size.array":      "The {label} field must contain {0} items.",
	"between.string":  "The {label} field must be between {0} and {1} characters.",
	"between.numeric": "The {label} field must be between {0} and {1}.",
	"between.array":   "The {label} field must have between {0} and {1} items.",

	// Formats.
	"email":       "The {label} field must be a valid email address.",
	"url":         "The {label} field must be a valid URL.",
	"uuid":        "The {label} field must be a valid UUID.",
	"alpha":       "The {label} field must only contain letters.",
	"alpha_num":   "The {label} field must only contain letters and numbers.",
	"alpha_dash":  "The {label} field must only contain letters, numbers, dashes, and underscores.",
	"ascii":       "The {label} field must only contain ASCII characters.",
	"numeric":     "The {label} field must be a number.",
	"integer":     "The {label} field must be an integer.",
	"lowercase":   "The {label} field must be lowercase.",
	"uppercase":   "The {label} field must be uppercase.",
	"starts_with": "The {label} field must start with one of the following: {list}.",
	"ends_with":   "The {label} field must end with one of the following: {list}.",
	"ip":          "The {label} field must be a valid IP address.",
	"ipv4":        "The {label} field must be a valid IPv4 address.",
	"ipv6":        "The {label} field must be a valid IPv6 address.",
	"json":        "The {label} field must be a valid JSON string.",
	"date":        "The {label} field must be a valid date (YYYY-MM-DD).",
	"datetime":    "The {label} field must be a valid date and time (RFC 3339).",

	// Choice.
	"in":       "The selected {label} is invalid.",
	"not_in":   "The selected {label} is invalid.",
	"distinct": "The {label} field has a duplicate value.",

	// Comparison.
	"same":            "The {label} field must match {0}.",
	"different":       "The {label} field and {0} must be different.",
	"confirmed":       "The {label} field confirmation does not match.",
	"after":           "The {label} field must be a date after {0}.",
	"after_or_equal":  "The {label} field must be a date after or equal to {0}.",
	"before":          "The {label} field must be a date before {0}.",
	"before_or_equal": "The {label} field must be a date before or equal to {0}.",

	// Files.
	"max_size":   "The {label} field must not be greater than {0}.",
	"min_size":   "The {label} field must be at least {0}.",
	"mimetypes":  "The {label} field must be a file of type: {list}.",
	"extensions": "The {label} field must have one of the following extensions: {list}.",
	"image":      "The {label} field must be an image.",

	// Custom rules registered without a message.
	"custom": "The {label} field is invalid.",
}
