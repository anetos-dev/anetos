// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Role says who wrote a [Message].
type Role string

// The roles of a conversation. System instructions aren't a message:
// they are [Request.System] ([System], [Agent.Instructions]).
const (
	RoleUser      Role = "user"      // the person, or the app on their behalf
	RoleAssistant Role = "assistant" // the model
	RoleTool      Role = "tool"      // the results of the model's tool calls
)

// Message is one turn of a conversation: the user's prompt, the model's
// answer (text and tool calls), or the results of those tool calls.
// Messages marshal to JSON, so a conversation can be stored and
// continued later ([Messages]).
type Message struct {
	// Role says who wrote it.
	Role Role
	// Parts are its content, in order.
	Parts []Part
}

// UserMessage returns a user message of text.
func UserMessage(text string) Message { return Message{Role: RoleUser, Parts: []Part{Text(text)}} }

// AssistantMessage returns a model message of text, for a conversation
// written by hand (examples in a prompt, tests).
func AssistantMessage(text string) Message {
	return Message{Role: RoleAssistant, Parts: []Part{Text(text)}}
}

// Text returns the message's text parts, joined.
func (m Message) Text() string {
	var b strings.Builder
	for _, p := range m.Parts {
		if t, ok := p.(Text); ok {
			b.WriteString(string(t))
		}
	}
	return b.String()
}

// ToolCalls returns the tool calls in the message, in order.
func (m Message) ToolCalls() []ToolCall {
	var out []ToolCall
	for _, p := range m.Parts {
		if c, ok := p.(ToolCall); ok {
			out = append(out, c)
		}
	}
	return out
}

// Part is a piece of a [Message]: [Text], [ToolCall] or [ToolResult].
// The set is closed: providers handle every kind there is.
type Part interface{ isPart() }

// Text is text, from the user or the model.
type Text string

// ToolCall is the model asking to run a tool.
type ToolCall struct {
	// ID identifies the call; its result carries it back.
	ID string
	// Name is the tool's.
	Name string
	// Input is the tool's input, a JSON object.
	Input json.RawMessage
}

// ToolResult is what a tool call returned, sent back to the model.
type ToolResult struct {
	// CallID is the ToolCall's ID.
	CallID string
	// Name is the tool's.
	Name string
	// Content is the tool's output: JSON, or text.
	Content string
	// IsError says Content describes why the call failed (invalid input,
	// not allowed, not found), for the model to correct or report.
	IsError bool
}

func (Text) isPart()       {}
func (ToolCall) isPart()   {}
func (ToolResult) isPart() {}

// partJSON is a Part's JSON form: {"type": "text", "text": "…"} and so on.
type partJSON struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	ID      string          `json:"id,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Content string          `json:"content,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
}

type messageJSON struct {
	Role  Role       `json:"role"`
	Parts []partJSON `json:"parts"`
}

// MarshalJSON writes the message as {"role": …, "parts": [{"type":
// "text" | "tool_call" | "tool_result", …}]}.
func (m Message) MarshalJSON() ([]byte, error) {
	out := messageJSON{Role: m.Role, Parts: make([]partJSON, len(m.Parts))}
	for i, p := range m.Parts {
		switch p := p.(type) {
		case Text:
			out.Parts[i] = partJSON{Type: "text", Text: string(p)}
		case ToolCall:
			input := p.Input
			if len(input) > 0 && !json.Valid(input) {
				// A provider passed on a model's broken JSON: keep it, as
				// a string, so the conversation still marshals.
				input, _ = json.Marshal(string(input))
			}
			out.Parts[i] = partJSON{Type: "tool_call", ID: p.ID, Name: p.Name, Input: input}
		case ToolResult:
			out.Parts[i] = partJSON{Type: "tool_result", CallID: p.CallID, Name: p.Name, Content: p.Content, IsError: p.IsError}
		default:
			return nil, fmt.Errorf("ai: unknown message part %T", p)
		}
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads what MarshalJSON writes.
func (m *Message) UnmarshalJSON(data []byte) error {
	var in messageJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	switch in.Role {
	case RoleUser, RoleAssistant, RoleTool:
	default:
		return fmt.Errorf("ai: unknown message role %q", in.Role)
	}
	parts := make([]Part, len(in.Parts))
	for i, p := range in.Parts {
		switch p.Type {
		case "text":
			parts[i] = Text(p.Text)
		case "tool_call":
			parts[i] = ToolCall{ID: p.ID, Name: p.Name, Input: p.Input}
		case "tool_result":
			parts[i] = ToolResult{CallID: p.CallID, Name: p.Name, Content: p.Content, IsError: p.IsError}
		default:
			return fmt.Errorf("ai: unknown message part type %q", p.Type)
		}
	}
	*m = Message{Role: in.Role, Parts: parts}
	return nil
}

// checkMessages reports messages a provider couldn't send: unknown
// roles or parts, and parts in the wrong role.
func checkMessages(msgs []Message) error {
	for i, m := range msgs {
		for _, p := range m.Parts {
			ok := false
			switch p.(type) {
			case Text:
				ok = m.Role == RoleUser || m.Role == RoleAssistant
			case ToolCall:
				ok = m.Role == RoleAssistant
			case ToolResult:
				ok = m.Role == RoleTool
			}
			if !ok {
				return fmt.Errorf("ai: message %d: a %s message can't hold %T", i, m.Role, p)
			}
		}
		if len(m.Parts) == 0 && m.Role != RoleAssistant { // a model may answer nothing
			return fmt.Errorf("ai: message %d (%s) is empty", i, m.Role)
		}
	}
	return nil
}

var errNothingToSend = errors.New("ai: nothing to send: give a prompt, or messages (ai.Messages)")
