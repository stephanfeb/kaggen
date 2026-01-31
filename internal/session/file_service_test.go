package session

import (
	"context"
	"os"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
)

func newTestEvent(author, content string, role model.Role) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    role,
						Content: content,
					},
				},
			},
			Created: time.Now().Unix(),
		},
		ID:           "evt-" + content,
		InvocationID: "inv-1",
		Author:       author,
		Timestamp:    time.Now(),
	}
}

func TestFileService_CreateAndGetSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-fs-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	svc := NewFileService(tmpDir)
	ctx := context.Background()
	key := trpcsession.Key{AppName: "testapp", UserID: "user1", SessionID: "sess1"}

	// Create session
	sess, err := svc.CreateSession(ctx, key, trpcsession.StateMap{"k1": []byte("v1")})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sess == nil {
		t.Fatal("expected non-nil session")
	}
	if sess.ID != "sess1" {
		t.Errorf("expected session ID sess1, got %s", sess.ID)
	}

	// Get session
	got, err := svc.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil session from get")
	}
	if got.ID != "sess1" {
		t.Errorf("expected session ID sess1, got %s", got.ID)
	}
}

func TestFileService_AppendEventAndReload(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-fs-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	svc := NewFileService(tmpDir)
	ctx := context.Background()
	key := trpcsession.Key{AppName: "testapp", UserID: "user1", SessionID: "sess1"}

	sess, err := svc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Append events
	evt1 := newTestEvent("user", "Hello", model.RoleUser)
	if err := svc.AppendEvent(ctx, sess, evt1); err != nil {
		t.Fatalf("append event 1: %v", err)
	}

	evt2 := newTestEvent("kaggen", "Hi there!", model.RoleAssistant)
	if err := svc.AppendEvent(ctx, sess, evt2); err != nil {
		t.Fatalf("append event 2: %v", err)
	}

	// Reload session from disk (simulating restart)
	svc2 := NewFileService(tmpDir)
	reloaded, err := svc2.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("get session after reload: %v", err)
	}
	if reloaded == nil {
		t.Fatal("expected non-nil session after reload")
	}

	events := reloaded.GetEvents()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestFileService_DeleteSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-fs-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	svc := NewFileService(tmpDir)
	ctx := context.Background()
	key := trpcsession.Key{AppName: "testapp", UserID: "user1", SessionID: "sess1"}

	_, err = svc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := svc.DeleteSession(ctx, key); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	got, err := svc.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil session after delete")
	}
}

func TestFileService_ListSessions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-fs-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	svc := NewFileService(tmpDir)
	ctx := context.Background()
	userKey := trpcsession.UserKey{AppName: "testapp", UserID: "user1"}

	for _, sid := range []string{"s1", "s2", "s3"} {
		key := trpcsession.Key{AppName: "testapp", UserID: "user1", SessionID: sid}
		if _, err := svc.CreateSession(ctx, key, nil); err != nil {
			t.Fatalf("create session %s: %v", sid, err)
		}
	}

	sessions, err := svc.ListSessions(ctx, userKey)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}
}

func TestFileService_AppState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-fs-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	svc := NewFileService(tmpDir)
	ctx := context.Background()

	if err := svc.UpdateAppState(ctx, "myapp", trpcsession.StateMap{"key1": []byte("val1")}); err != nil {
		t.Fatalf("update app state: %v", err)
	}

	state, err := svc.ListAppStates(ctx, "myapp")
	if err != nil {
		t.Fatalf("list app states: %v", err)
	}
	if string(state["key1"]) != "val1" {
		t.Errorf("expected val1, got %s", string(state["key1"]))
	}

	if err := svc.DeleteAppState(ctx, "myapp", "key1"); err != nil {
		t.Fatalf("delete app state: %v", err)
	}

	state, err = svc.ListAppStates(ctx, "myapp")
	if err != nil {
		t.Fatalf("list app states after delete: %v", err)
	}
	if _, ok := state["key1"]; ok {
		t.Error("expected key1 to be deleted")
	}
}

func TestFileService_UserState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-fs-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	svc := NewFileService(tmpDir)
	ctx := context.Background()
	userKey := trpcsession.UserKey{AppName: "myapp", UserID: "user1"}

	if err := svc.UpdateUserState(ctx, userKey, trpcsession.StateMap{"pref": []byte("dark")}); err != nil {
		t.Fatalf("update user state: %v", err)
	}

	state, err := svc.ListUserStates(ctx, userKey)
	if err != nil {
		t.Fatalf("list user states: %v", err)
	}
	if string(state["pref"]) != "dark" {
		t.Errorf("expected dark, got %s", string(state["pref"]))
	}
}

func TestFileService_SessionState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-fs-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	svc := NewFileService(tmpDir)
	ctx := context.Background()
	key := trpcsession.Key{AppName: "myapp", UserID: "user1", SessionID: "s1"}

	_, err = svc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := svc.UpdateSessionState(ctx, key, trpcsession.StateMap{"foo": []byte("bar")}); err != nil {
		t.Fatalf("update session state: %v", err)
	}

	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	v, ok := sess.GetState("foo")
	if !ok || string(v) != "bar" {
		t.Errorf("expected state foo=bar, got ok=%v v=%s", ok, string(v))
	}
}

func TestFileService_GetNonExistent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-fs-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	svc := NewFileService(tmpDir)
	ctx := context.Background()
	key := trpcsession.Key{AppName: "testapp", UserID: "user1", SessionID: "nonexistent"}

	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("get nonexistent: %v", err)
	}
	if sess != nil {
		t.Error("expected nil session for nonexistent")
	}
}

func TestFileService_Close(t *testing.T) {
	svc := NewFileService("/tmp/unused")
	if err := svc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestReadEventJSONL_NonExistent(t *testing.T) {
	events, _, err := ReadEventJSONL("/nonexistent/path.jsonl")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if events != nil {
		t.Fatalf("expected nil events, got: %v", events)
	}
}

func TestReadEventJSONL_RoundTrip(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-jsonl-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	path := tmpDir + "/test.jsonl"
	evt := newTestEvent("user", "hello", model.RoleUser)
	if err := AppendEventJSONL(path, evt); err != nil {
		t.Fatalf("append: %v", err)
	}

	events, _, err := ReadEventJSONL(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Author != "user" {
		t.Errorf("expected author user, got %s", events[0].Author)
	}
}
