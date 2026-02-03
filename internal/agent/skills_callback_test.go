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
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// TestSubAgentCallbacksInvoked verifies that callbacks passed via
// llmagent.WithToolCallbacks are actually invoked when the sub-agent runs tools.
// This test is designed to isolate the specific issue: do callbacks fire when
// a tool is executed by a sub-agent created with WithToolCallbacks?
func TestSubAgentCallbacksInvoked(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Counter to track if BeforeTool callback was called
	var callbackInvoked atomic.Int32
	var invokedTools []string
	var toolsMu sync.Mutex

	// Create callbacks
	callbacks := &tool.Callbacks{}
	callbacks.RegisterBeforeTool(tool.BeforeToolCallbackStructured(
		func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
			t.Logf("BeforeTool callback INVOKED for tool: %s", args.ToolName)
			callbackInvoked.Add(1)
			toolsMu.Lock()
			invokedTools = append(invokedTools, args.ToolName)
			toolsMu.Unlock()
			return &tool.BeforeToolResult{}, nil
		},
	))

	// Create a simple tool that the agent can use
	echoTool := function.NewFunctionTool(
		echoToolFuncForCallback,
		function.WithName("echo_test"),
		function.WithDescription("Echo a message for testing"),
	)

	// Create a mock model that will always call the echo_test tool
	mockModel := &callbackTestModel{}

	// Create sub-agent WITH callbacks
	subAgent := llmagent.New("test-skill",
		llmagent.WithModel(mockModel),
		llmagent.WithInstruction("You are a test agent."),
		llmagent.WithDescription("Test agent for callback verification"),
		llmagent.WithTools([]tool.Tool{echoTool}),
		llmagent.WithToolCallbacks(callbacks),
		llmagent.WithMaxLLMCalls(3),
		llmagent.WithMaxToolIterations(3),
	)

	// Create invocation
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(subAgent),
		agent.WithInvocationMessage(model.Message{
			Role:    model.RoleUser,
			Content: "Please echo a test message",
		}),
		agent.WithInvocationModel(mockModel),
	)

	// Run the agent
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	events, err := subAgent.Run(ctx, inv)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Drain events
	eventCount := 0
	for evt := range events {
		if evt != nil {
			eventCount++
			t.Logf("Event #%d received", eventCount)
		}
	}

	// Verify callback was invoked
	count := callbackInvoked.Load()
	t.Logf("Callback invocation count: %d", count)
	t.Logf("Tools that triggered callbacks: %v", invokedTools)
	t.Logf("Mock model call count: %d", mockModel.getCallCount())

	if count == 0 {
		t.Errorf("BUG CONFIRMED: BeforeTool callback was NOT invoked. llmagent.WithToolCallbacks does not work for sub-agents when they run tools.")
	} else {
		t.Logf("SUCCESS: BeforeTool callback was invoked %d times", count)
	}

	logger.Info("test complete", "callback_count", count)
}

// echoToolRequestCallback is the input schema for our test tool.
type echoToolRequestCallback struct {
	Message string `json:"message" jsonschema:"required,description=Message to echo"`
}

// echoToolResponseCallback is the output of our test tool.
type echoToolResponseCallback struct {
	Result string `json:"result"`
}

func echoToolFuncForCallback(_ context.Context, req echoToolRequestCallback) (echoToolResponseCallback, error) {
	return echoToolResponseCallback{Result: "echoed: " + req.Message}, nil
}

// callbackTestModel is a minimal mock that always returns a tool call then stops
type callbackTestModel struct {
	mu      sync.Mutex
	callNum int
}

func (m *callbackTestModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.callNum++
	call := m.callNum
	m.mu.Unlock()

	ch := make(chan *model.Response, 1)

	if call == 1 {
		// First call: return a tool call
		ch <- &model.Response{
			ID:    "resp-1",
			Model: "mock",
			Done:  true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "I'll echo the message.",
					ToolCalls: []model.ToolCall{{
						Type: "function",
						ID:   "call-1",
						Function: model.FunctionDefinitionParam{
							Name:      "echo_test",
							Arguments: []byte(`{"message":"hello from callback test"}`),
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
					Content: "Done! The echo is complete.",
				},
				FinishReason: strPtrCallback("stop"),
			}},
		}
	}
	close(ch)
	return ch, nil
}

func (m *callbackTestModel) Info() model.Info {
	return model.Info{Name: "callback-test-mock"}
}

func (m *callbackTestModel) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callNum
}

func strPtrCallback(s string) *string { return &s }
