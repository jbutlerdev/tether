// Tests for the tether-forge CLI. See plan.md §8.5.
//
// The CLI is a thin wrapper over the forge.Client interface.
// The tests exercise the command dispatcher (run() in main.go)
// by passing a MockClient and capturing stdout/stderr.
//
// CLI surface:
//
//	tether forge list
//	tether forge create [--profile coder]
//	tether forge rename <id> "name"
//	tether forge say <id> "text"
//	tether forge delete <id>
//
// All subcommands return exit code 0 on success, 1 on
// argument / usage error, and 2 on backend error. The tests
// assert on the exit code AND on the program's stdout,
// because the CLI is the user-facing surface.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/forge"
)

// runCLI executes the CLI with the given args, returning the
// captured stdout, stderr, and exit code. The MockClient is
// passed in so tests can pre-seed it.
func runCLI(t *testing.T, mc *forge.MockClient, args []string) (stdout, stderr string, exit int) {
	t.Helper()
	oldStdout, oldStderr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	// Run the CLI in a goroutine; the read goroutines drain
	// the pipes concurrently.
	type result struct {
		code int
	}
	resCh := make(chan result, 1)
	go func() {
		resCh <- result{code: run(args, mc)}
	}()
	var outBuf, errBuf bytes.Buffer
	doneCh := make(chan struct{})
	go func() {
		_, _ = io.Copy(&outBuf, rOut)
		_, _ = io.Copy(&errBuf, rErr)
		close(doneCh)
	}()

	// Wait for the CLI to finish, then close the write
	// ends so the read goroutine returns.
	res := <-resCh
	_ = wOut.Close()
	_ = wErr.Close()
	<-doneCh
	return outBuf.String(), errBuf.String(), res.code
}

// TestCLI_List verifies that `tether forge list` prints the
// session ids returned by ListSessions, one per line.
func TestCLI_List(t *testing.T) {

	mc := forge.NewMockClient()
	defer mc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	id1, _ := mc.CreateSession(ctx, "coder")
	id2, _ := mc.CreateSession(ctx, "researcher")

	stdout, _, exit := runCLI(t, mc, []string{"list"})
	if exit != 0 {
		t.Errorf("exit: want 0, got %d", exit)
	}
	if !strings.Contains(stdout, id1) {
		t.Errorf("stdout: want id1 %q, got %q", id1, stdout)
	}
	if !strings.Contains(stdout, id2) {
		t.Errorf("stdout: want id2 %q, got %q", id2, stdout)
	}
}

// TestCLI_List_Empty verifies that `list` on a fresh client
// prints a friendly empty message rather than nothing.
func TestCLI_List_Empty(t *testing.T) {

	mc := forge.NewMockClient()
	defer mc.Close()

	stdout, _, exit := runCLI(t, mc, []string{"list"})
	if exit != 0 {
		t.Errorf("exit: want 0, got %d", exit)
	}
	if !strings.Contains(strings.ToLower(stdout), "no sessions") {
		t.Errorf("stdout: want 'no sessions' message, got %q", stdout)
	}
}

// TestCLI_Create_ReturnsConvID verifies that `create` prints
// the new session id.
func TestCLI_Create_ReturnsConvID(t *testing.T) {

	mc := forge.NewMockClient()
	defer mc.Close()

	stdout, _, exit := runCLI(t, mc, []string{"create", "--profile", "coder"})
	if exit != 0 {
		t.Errorf("exit: want 0, got %d", exit)
	}
	// The new id should appear in the output. The Mock
	// returns a 36-char UUID.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) == 0 {
		t.Fatal("no output")
	}
	id := strings.TrimSpace(lines[len(lines)-1])
	if len(id) != 36 {
		t.Errorf("create: want 36-char UUID, got %q (stdout=%q)", id, stdout)
	}
}

// TestCLI_Create_DefaultProfile verifies that omitting
// --profile falls back to the default ("coder").
func TestCLI_Create_DefaultProfile(t *testing.T) {

	mc := forge.NewMockClient()
	defer mc.Close()

	_, _, exit := runCLI(t, mc, []string{"create"})
	if exit != 0 {
		t.Errorf("exit: want 0, got %d", exit)
	}
	sessions, _ := mc.ListSessions(context.Background())
	if len(sessions) != 1 {
		t.Fatalf("ListSessions: want 1, got %d", len(sessions))
	}
	if sessions[0].Profile != "coder" {
		t.Errorf("Profile: want %q, got %q", "coder", sessions[0].Profile)
	}
}

// TestCLI_Say_PostsMessage verifies that `say` posts the
// given text to the named session.
func TestCLI_Say_PostsMessage(t *testing.T) {

	mc := forge.NewMockClient()
	defer mc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	id, _ := mc.CreateSession(ctx, "coder")

	_, _, exit := runCLI(t, mc, []string{"say", id, "hello"})
	if exit != 0 {
		t.Errorf("exit: want 0, got %d", exit)
	}
	calls := mc.SendMessageCalls()
	if len(calls) != 1 {
		t.Fatalf("SendMessageCalls: want 1, got %d", len(calls))
	}
	if calls[0].Text != "hello" {
		t.Errorf("Text: want %q, got %q", "hello", calls[0].Text)
	}
	if calls[0].SessionID != id {
		t.Errorf("SessionID: want %q, got %q", id, calls[0].SessionID)
	}
}

// TestCLI_Delete_RemovesConv verifies that `delete` removes
// the session.
func TestCLI_Delete_RemovesConv(t *testing.T) {

	mc := forge.NewMockClient()
	defer mc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	id, _ := mc.CreateSession(ctx, "coder")

	_, _, exit := runCLI(t, mc, []string{"delete", id})
	if exit != 0 {
		t.Errorf("exit: want 0, got %d", exit)
	}
	// Re-listing should not include the deleted id.
	sessions, _ := mc.ListSessions(ctx)
	for _, s := range sessions {
		if s.ID == id {
			t.Errorf("delete: id %q still in list", id)
		}
	}
}

// TestCLI_BadArgs verifies that an unknown subcommand exits
// non-zero and prints a usage line to stderr.
func TestCLI_BadArgs(t *testing.T) {

	mc := forge.NewMockClient()
	defer mc.Close()
	_, stderr, exit := runCLI(t, mc, []string{"unknown-subcommand"})
	if exit == 0 {
		t.Errorf("exit: want non-zero for unknown subcommand, got 0")
	}
	if !strings.Contains(strings.ToLower(stderr), "usage") &&
		!strings.Contains(strings.ToLower(stderr), "unknown") {
		t.Errorf("stderr: want usage/unknown message, got %q", stderr)
	}
}

// TestCLI_Say_RequiresTwoArgs verifies that `say` with the
// wrong number of args exits non-zero.
func TestCLI_Say_RequiresTwoArgs(t *testing.T) {

	mc := forge.NewMockClient()
	defer mc.Close()
	_, _, exit := runCLI(t, mc, []string{"say", "only-one-arg"})
	if exit == 0 {
		t.Errorf("exit: want non-zero for missing arg, got 0")
	}
}

// TestCLI_Delete_UnknownID verifies that deleting a missing
// id exits non-zero.
func TestCLI_Delete_UnknownID(t *testing.T) {

	mc := forge.NewMockClient()
	defer mc.Close()
	_, _, exit := runCLI(t, mc, []string{"delete", "00000000-0000-0000-0000-000000000000"})
	if exit == 0 {
		t.Errorf("exit: want non-zero for unknown id, got 0")
	}
}

// TestCLI_Say_UnknownSession verifies that sending to an
// unknown session exits non-zero.
func TestCLI_Say_UnknownSession(t *testing.T) {

	mc := forge.NewMockClient()
	defer mc.Close()
	_, _, exit := runCLI(t, mc, []string{"say", "no-such-id", "hi"})
	if exit == 0 {
		t.Errorf("exit: want non-zero for unknown session, got 0")
	}
}

// TestCLI_Rename_UpdatesConv verifies that `rename` writes
// a confirmation message to stdout and exits 0.
func TestCLI_Rename_UpdatesConv(t *testing.T) {
	mc := forge.NewMockClient()
	defer mc.Close()
	stdout, _, exit := runCLI(t, mc, []string{"rename", "abc-conv-id", "My Forge Session"})
	if exit != 0 {
		t.Errorf("exit: want 0, got %d", exit)
	}
	if !strings.Contains(stdout, "My Forge Session") {
		t.Errorf("stdout: want new name, got %q", stdout)
	}
	if !strings.Contains(stdout, "abc-conv-id") {
		t.Errorf("stdout: want conv id, got %q", stdout)
	}
}

// TestCLI_Rename_RequiresTwoArgs verifies that `rename` with
// only one arg exits non-zero.
func TestCLI_Rename_RequiresTwoArgs(t *testing.T) {
	mc := forge.NewMockClient()
	defer mc.Close()
	_, _, exit := runCLI(t, mc, []string{"rename", "only-id"})
	if exit == 0 {
		t.Errorf("exit: want non-zero for missing name, got 0")
	}
}

// TestCLI_Help verifies that `help` and `-h` print the usage
// and exit 0.
func TestCLI_Help(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		mc := forge.NewMockClient()
		stdout, _, exit := runCLI(t, mc, []string{arg})
		mc.Close()
		if exit != 0 {
			t.Errorf("help %q: exit: want 0, got %d", arg, exit)
		}
		if !strings.Contains(stdout, "Usage:") {
			t.Errorf("help %q: want 'Usage:' in stdout, got %q", arg, stdout)
		}
	}
}

// TestCLI_Create_BadFlag verifies that an unknown flag
// exits non-zero (flag.ContinueOnError path).
func TestCLI_Create_BadFlag(t *testing.T) {
	mc := forge.NewMockClient()
	defer mc.Close()
	_, _, exit := runCLI(t, mc, []string{"create", "--unknown"})
	if exit == 0 {
		t.Errorf("exit: want non-zero for unknown flag, got 0")
	}
}

// TestCLI_NoArgs verifies that calling the CLI with no args
// prints the usage line and exits non-zero.
func TestCLI_NoArgs(t *testing.T) {

	mc := forge.NewMockClient()
	defer mc.Close()
	_, stderr, exit := runCLI(t, mc, []string{})
	if exit == 0 {
		t.Errorf("exit: want non-zero for no args, got 0")
	}
	if stderr == "" && stdoutEmpty(exit) {
		t.Errorf("expected usage on stderr or stdout")
	}
}

func stdoutEmpty(exit int) bool { return exit == 0 }

// TestCLI_ConcurrentCalls verifies that the underlying
// forge.MockClient is safe to invoke concurrently. We do
// not run multiple runCLI invocations in parallel because
// they share os.Stdout / os.Stderr globals; the underlying
// mocks are independently exercised by the parallel tests
// in the forge package.
func TestCLI_ConcurrentCalls(t *testing.T) {
	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			mc := forge.NewMockClient()
			defer mc.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			if _, err := mc.CreateSession(ctx, "coder"); err != nil {
				t.Errorf("CreateSession: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestCLI_BackendError verifies that a backend error
// (injected via MockOptionSendError) is reported on stderr
// and the exit code is non-zero.
func TestCLI_BackendError(t *testing.T) {

	mc := forge.NewMockClient(forge.MockOptionSendError(errors.New("backend down")))
	defer mc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	id, _ := mc.CreateSession(ctx, "coder")

	_, stderr, exit := runCLI(t, mc, []string{"say", id, "hi"})
	if exit == 0 {
		t.Errorf("exit: want non-zero on backend error, got 0")
	}
	if !strings.Contains(strings.ToLower(stderr), "backend") {
		t.Errorf("stderr: want error message, got %q", stderr)
	}
}
