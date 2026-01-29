package agent

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// multiTurnModel is a mock model that returns a tool call on the first call,
// then a final text response on the second call. This simulates the expected
// two-step behavior: "call a tool" → "produce final answer".
type multiTurnModel struct {
	mu       sync.Mutex
	callNum  int
	requests []*model.Request // captured requests for assertions
}

func (m *multiTurnModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.callNum++
	call := m.callNum
	m.requests = append(m.requests, req)
	m.mu.Unlock()

	ch := make(chan *model.Response, 1)

	if call == 1 {
		// First call: return a tool call (simulates "mkdir")
		ch <- &model.Response{
			ID:    "resp-1",
			Model: "mock",
			Done:  true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "I'll create the project now.",
					ToolCalls: []model.ToolCall{{
						Type: "function",
						ID:   "call-1",
						Function: model.FunctionDefinitionParam{
							Name:      "echo_tool",
							Arguments: []byte(`{"text":"hello"}`),
						},
					}},
				},
			}},
		}
	} else {
		// Subsequent calls: return final text response (no tool calls)
		ch <- &model.Response{
			ID:    "resp-final",
			Model: "mock",
			Done:  true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Done! The project is complete.",
				},
				FinishReason: strPtr("stop"),
			}},
		}
	}
	close(ch)
	return ch, nil
}

func (m *multiTurnModel) Info() model.Info {
	return model.Info{Name: "multi-turn-mock"}
}

func (m *multiTurnModel) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callNum
}

func (m *multiTurnModel) getRequests() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*model.Request, len(m.requests))
	copy(cp, m.requests)
	return cp
}

func strPtr(s string) *string { return &s }

// echoToolRequest is the input schema for our test tool.
type echoToolRequest struct {
	Text string `json:"text" jsonschema:"required,description=Text to echo"`
}

// echoToolResponse is the output of our test tool.
type echoToolResponse struct {
	Result string `json:"result"`
}

func echoToolFunc(_ context.Context, req echoToolRequest) (echoToolResponse, error) {
	return echoToolResponse{Result: "echoed: " + req.Text}, nil
}

// TestAsyncDispatchMultiTurn verifies that an async sub-agent sees accumulated
// conversation history across flow loop iterations. This is the core test for
// the infinite loop bug — if the agent doesn't see previous tool calls, it
// repeats the first step forever.
func TestAsyncDispatchMultiTurn(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockModel := &multiTurnModel{}

	// Create a simple tool
	echoTool := function.NewFunctionTool(
		echoToolFunc,
		function.WithName("echo_tool"),
		function.WithDescription("Echoes text back"),
	)

	// Create an llmagent with the mock model and tool
	ag := llmagent.New("test-coder",
		llmagent.WithModel(mockModel),
		llmagent.WithTools([]tool.Tool{echoTool}),
		llmagent.WithInstruction("You are a test agent. Use the echo_tool when asked."),
		llmagent.WithMaxLLMCalls(5),
		llmagent.WithMaxToolIterations(3),
	)

	store := NewInFlightStore()
	var completedTaskID string
	var completedResult string
	var completedErr error
	var completionCalled atomic.Bool

	completeFn := func(taskID, result string, err error, policy TriggerPolicy) {
		completedTaskID = taskID
		completedResult = result
		completedErr = err
		completionCalled.Store(true)
	}

	agentMap := map[string]agent.Agent{
		"test-coder": ag,
	}

	d := &asyncDispatcher{
		agents:     agentMap,
		store:      store,
		completeFn: completeFn,
		model:      mockModel,
		logger:     logger,
	}

	// Dispatch the task
	resp, err := d.dispatch(context.Background(), asyncDispatchRequest{
		AgentName: "test-coder",
		Task:      "Create a project for me",
		Policy:    "auto",
	})
	if err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}

	t.Logf("dispatched task: %s", resp.TaskID)

	// Wait for completion (with timeout)
	deadline := time.After(30 * time.Second)
	for !completionCalled.Load() {
		select {
		case <-deadline:
			t.Fatalf("task did not complete within 30s — likely stuck in a loop. Model was called %d times", mockModel.getCallCount())
		case <-time.After(100 * time.Millisecond):
		}
	}

	t.Logf("task completed: id=%s result=%q err=%v", completedTaskID, completedResult, completedErr)
	t.Logf("model was called %d times", mockModel.getCallCount())

	// Assertions
	if completedErr != nil {
		t.Errorf("expected no error, got: %v", completedErr)
	}

	callCount := mockModel.getCallCount()
	if callCount < 2 {
		t.Errorf("expected at least 2 model calls (tool call + final), got %d", callCount)
	}
	if callCount > 5 {
		t.Errorf("expected at most 5 model calls (within limits), got %d — agent is looping", callCount)
	}

	// The key assertion: the second request should contain messages from the
	// first turn (tool call + tool result). If session.Events isn't being
	// populated, the second request will only have the system prompt + user
	// message.
	requests := mockModel.getRequests()
	if len(requests) >= 2 {
		secondReq := requests[1]
		t.Logf("second request has %d messages", len(secondReq.Messages))
		for i, msg := range secondReq.Messages {
			t.Logf("  msg[%d]: role=%s content_len=%d tool_calls=%d ",
				i, msg.Role, len(msg.Content), len(msg.ToolCalls))
		}

		// The second request should have more messages than just system + user.
		// If working correctly: system, user, assistant (with tool call), tool result
		if len(secondReq.Messages) <= 2 {
			t.Errorf("second LLM request only has %d messages — history is NOT accumulating! "+
				"Expected at least 4 (system + user + assistant + tool result)", len(secondReq.Messages))
		}
	}

	// Verify task state
	ts, ok := store.Get(resp.TaskID)
	if !ok {
		t.Fatalf("task not found in store")
	}
	t.Logf("task status: %s, result: %q, events: %d", ts.Status, ts.Result, len(ts.Events))
}

// TestAsyncDispatchLoopDetection verifies that a model which always returns
// tool calls will eventually stop (via MaxLLMCalls/MaxToolIterations) rather
// than looping forever.
func TestAsyncDispatchLoopDetection(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// This model ALWAYS returns a tool call — simulating the bug scenario
	alwaysToolCall := &alwaysToolCallModel{}

	echoTool := function.NewFunctionTool(
		echoToolFunc,
		function.WithName("echo_tool"),
		function.WithDescription("Echoes text back"),
	)

	ag := llmagent.New("loop-agent",
		llmagent.WithModel(alwaysToolCall),
		llmagent.WithTools([]tool.Tool{echoTool}),
		llmagent.WithInstruction("You are a test agent."),
		llmagent.WithMaxLLMCalls(5),
		llmagent.WithMaxToolIterations(3),
	)

	store := NewInFlightStore()
	var completionCalled atomic.Bool

	completeFn := func(taskID, result string, err error, policy TriggerPolicy) {
		completionCalled.Store(true)
	}

	d := &asyncDispatcher{
		agents:     map[string]agent.Agent{"loop-agent": ag},
		store:      store,
		completeFn: completeFn,
		model:      alwaysToolCall,
		logger:     logger,
	}

	_, dispErr := d.dispatch(context.Background(), asyncDispatchRequest{
		AgentName: "loop-agent",
		Task:      "Do something",
		Policy:    "auto",
	})
	if dispErr != nil {
		t.Fatalf("dispatch failed: %v", dispErr)
	}

	deadline := time.After(30 * time.Second)
	for !completionCalled.Load() {
		select {
		case <-deadline:
			t.Fatalf("task did not complete within 30s — safety limits not enforced. Model called %d times",
				alwaysToolCall.getCallCount())
		case <-time.After(100 * time.Millisecond):
		}
	}

	callCount := alwaysToolCall.getCallCount()
	t.Logf("model was called %d times before stopping", callCount)

	if callCount > 10 {
		t.Errorf("expected agent to stop within ~5 calls (MaxLLMCalls=5), but it was called %d times", callCount)
	}
}

// alwaysToolCallModel always returns a tool call, never a final response.
type alwaysToolCallModel struct {
	mu      sync.Mutex
	callNum int
}

func (m *alwaysToolCallModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.callNum++
	call := m.callNum
	m.mu.Unlock()

	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:    "resp-loop-" + string(rune('0'+call)),
		Model: "mock",
		Done:  true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "Let me do something.",
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   "call-" + string(rune('0'+call)),
					Function: model.FunctionDefinitionParam{
						Name:      "echo_tool",
						Arguments: []byte(`{"text":"step"}`),
					},
				}},
			},
		}},
	}
	close(ch)
	return ch, nil
}

func (m *alwaysToolCallModel) Info() model.Info {
	return model.Info{Name: "always-tool-call-mock"}
}

func (m *alwaysToolCallModel) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callNum
}

// TestLLMAgentDirectMultiTurn tests the llmagent directly (no async dispatch)
// to isolate whether the issue is in the flow loop or the async wrapper.
func TestLLMAgentDirectMultiTurn(t *testing.T) {
	mockModel := &multiTurnModel{}

	echoTool := function.NewFunctionTool(
		echoToolFunc,
		function.WithName("echo_tool"),
		function.WithDescription("Echoes text back"),
	)

	ag := llmagent.New("direct-agent",
		llmagent.WithModel(mockModel),
		llmagent.WithTools([]tool.Tool{echoTool}),
		llmagent.WithInstruction("You are a test agent."),
		llmagent.WithMaxLLMCalls(5),
		llmagent.WithMaxToolIterations(3),
	)

	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationMessage(model.Message{
			Role:    model.RoleUser,
			Content: "Create a project for me",
		}),
		agent.WithInvocationModel(mockModel),
		agent.WithInvocationSession(&trpcsession.Session{
			ID:     "test-session",
			UserID: "test-user",
		}),
	)

	// Seed session with user message event — required by ApplyEventFiltering
	// which wipes session.Events if no user message is present.
	userEvt := event.NewResponseEvent(inv.InvocationID, "user", &model.Response{
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: model.RoleUser, Content: "Create a project for me"},
		}},
	})
	inv.Session.UpdateUserSession(userEvt)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = agent.NewInvocationContext(ctx, inv)

	evCh, err := ag.Run(ctx, inv)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var events []*event.Event
	for evt := range evCh {
		if evt != nil {
			events = append(events, evt)

			// Mimic the async dispatch: persist to session + notify
			beforeLen := len(inv.Session.Events)
			inv.Session.UpdateUserSession(evt)
			afterLen := len(inv.Session.Events)
			if afterLen > beforeLen {
				t.Logf("  >> UpdateUserSession ACCEPTED event %s (events: %d->%d)", evt.ID, beforeLen, afterLen)
			} else if evt.Response != nil && !evt.IsPartial && evt.IsValidContent() {
				t.Logf("  >> UpdateUserSession REJECTED event %s despite passing guard! resp=%p partial=%v valid=%v",
					evt.ID, evt.Response, evt.IsPartial, evt.IsValidContent())
			}
			if evt.RequiresCompletion {
				key := agent.GetAppendEventNoticeKey(evt.ID)
				_ = inv.NotifyCompletion(ctx, key)
			}
		}
	}

	t.Logf("received %d events", len(events))
	t.Logf("model called %d times", mockModel.getCallCount())
	t.Logf("session.Events has %d entries", len(inv.Session.Events))

	// Log event details
	for i, evt := range events {
		hasResp := evt.Response != nil
		var content string
		var toolCalls int
		var isPartial, isValidContent, isToolCall, isToolResult bool
		if hasResp {
			isPartial = evt.Response.IsPartial
			isValidContent = evt.Response.IsValidContent()
			isToolCall = evt.Response.IsToolCallResponse()
			isToolResult = evt.Response.IsToolResultResponse()
			if len(evt.Response.Choices) > 0 {
				content = evt.Response.Choices[0].Message.Content
				toolCalls = len(evt.Response.Choices[0].Message.ToolCalls)
			}
		}
		t.Logf("  event[%d]: id=%s hasResp=%v partial=%v validContent=%v toolCall=%v toolResult=%v content_len=%d toolCalls=%d reqComp=%v done=%v",
			i, evt.ID, hasResp, isPartial, isValidContent, isToolCall, isToolResult, len(content), toolCalls, evt.RequiresCompletion, hasResp && evt.Response.Done)
	}

	callCount := mockModel.getCallCount()
	if callCount < 2 {
		t.Errorf("expected at least 2 model calls, got %d", callCount)
	}
	if callCount > 5 {
		t.Errorf("model called %d times — looping", callCount)
	}

	// Check session events
	if len(inv.Session.Events) == 0 {
		t.Error("session.Events is empty — UpdateUserSession is not persisting events")
	} else {
		t.Logf("session.Events contents:")
		for i, evt := range inv.Session.Events {
			var content string
			var toolCalls int
			if len(evt.Choices) > 0 {
				content = evt.Choices[0].Message.Content
				toolCalls = len(evt.Choices[0].Message.ToolCalls)
			}
			t.Logf("  [%d]: id=%s content_len=%d toolCalls=%d reqID=%s invID=%s",
				i, evt.ID, len(content), toolCalls, evt.RequestID, evt.InvocationID)
		}
	}

	// Check second request's messages
	requests := mockModel.getRequests()
	if len(requests) >= 2 {
		t.Logf("second request messages:")
		for i, msg := range requests[1].Messages {
			t.Logf("  [%d]: role=%s content_len=%d toolCalls=%d toolCallID=%q",
				i, msg.Role, len(msg.Content), len(msg.ToolCalls), len(msg.ToolCalls))
		}
		if len(requests[1].Messages) <= 2 {
			t.Errorf("history not accumulating: second request only has %d messages", len(requests[1].Messages))
		}
	}
}
