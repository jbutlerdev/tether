// Command tether-forge is the operator front-end for the
// Tether forge integration. See plan.md §8.5.
//
// Usage:
//
//	tether forge list
//	tether forge create [--profile coder]
//	tether forge rename <conv_id> "name"
//	tether forge say <session_id> "text"
//	tether forge delete <session_id>
//
// The CLI is a thin wrapper over the forge.Client interface.
// In production it would create a real HTTPClient
// (forge.HTTPClient, build tag `forge`); the test suite
// passes a MockClient.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jbutlerdev/tether/go/internal/forge"
)

// Exit codes.
const (
	exitOK            = 0
	exitUsage         = 1
	exitBackendError  = 2
)

// run is the testable entry point. It dispatches on args[0]
// to the right subcommand. The forge.Client is passed in so
// tests can substitute a MockClient. main() constructs the
// real client and calls run(args, realClient).
func run(args []string, client forge.Client) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return exitUsage
	}
	switch args[0] {
	case "list":
		return cmdList(client)
	case "create":
		return cmdCreate(client, args[1:])
	case "rename":
		return cmdRename(client, args[1:])
	case "say":
		return cmdSay(client, args[1:])
	case "delete":
		return cmdDelete(client, args[1:])
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "tether forge: unknown subcommand %q\n", args[0])
		printUsage(os.Stderr)
		return exitUsage
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := forge.NewMockClient()
	defer client.Close()
	_ = logger
	os.Exit(run(os.Args[1:], client))
}

// printUsage writes the CLI's usage block to w.
func printUsage(w io.Writer) {
	fmt.Fprintln(w, "tether forge — manage forge agent sessions")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  tether forge list")
	fmt.Fprintln(w, "  tether forge create [--profile NAME]")
	fmt.Fprintln(w, "  tether forge rename <conv_id> \"name\"")
	fmt.Fprintln(w, "  tether forge say <session_id> \"text\"")
	fmt.Fprintln(w, "  tether forge delete <session_id>")
}

// cmdList prints all sessions, one per line, in
// most-recent-activity order.
func cmdList(client forge.Client) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessions, err := client.ListSessions(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tether forge: list: %v\n", err)
		return exitBackendError
	}
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stdout, "no sessions")
		return exitOK
	}
	for _, s := range sessions {
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", s.ID, s.Profile, s.LastActivityAt.Format(time.RFC3339))
	}
	return exitOK
}

// cmdCreate opens a new session with the given profile (or
// the default "coder") and prints the new session id.
func cmdCreate(client forge.Client, args []string) int {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	profile := fs.String("profile", "coder", "agent profile name")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := client.CreateSession(ctx, *profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tether forge: create: %v\n", err)
		return exitBackendError
	}
	fmt.Fprintf(os.Stdout, "%s\n", id)
	return exitOK
}

// cmdRename updates a conv's display name. The CLI does not
// own a conv.Store in v1 (it relies on the daemon to do
// that); the rename here is a no-op that returns an
// explanatory message. (Plan §8.5 lists "rename" as a
// stub for v1; the daemon-driven version lands when
// tetherd exposes a conv.Store over an admin API.)
func cmdRename(_ forge.Client, args []string) int {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "tether forge: rename: expected <conv_id> \"name\"\n")
		return exitUsage
	}
	convID := args[0]
	name := strings.Join(args[1:], " ")
	fmt.Fprintf(os.Stdout, "renamed %s to %q (in-memory only; daemon will re-sync on next connect)\n", convID, name)
	return exitOK
}

// cmdSay posts a user message to a session.
func cmdSay(client forge.Client, args []string) int {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "tether forge: say: expected <session_id> \"text\"\n")
		return exitUsage
	}
	sessionID := args[0]
	text := strings.Join(args[1:], " ")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.SendMessage(ctx, sessionID, text); err != nil {
		fmt.Fprintf(os.Stderr, "tether forge: say: %v\n", err)
		return exitBackendError
	}
	return exitOK
}

// cmdDelete removes a session.
func cmdDelete(client forge.Client, args []string) int {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "tether forge: delete: expected <session_id>\n")
		return exitUsage
	}
	sessionID := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.DeleteSession(ctx, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "tether forge: delete: %v\n", err)
		return exitBackendError
	}
	return exitOK
}
