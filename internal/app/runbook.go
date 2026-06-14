package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	relayRunbookMarkerStart = "<!-- coderoam:relay:start -->"
	relayRunbookMarkerEnd   = "<!-- coderoam:relay:end -->"
)

// relayRunbook is the agent-agnostic runbook coderoam writes into each agent's
// instruction file so a cold session acts immediately instead of exploring the
// code/docs. It avoids backticks so it can live in a Go raw string literal.
const relayRunbook = `# coderoam - WhatsApp relay runbook

coderoam bridges WhatsApp groups to THIS coding session. When asked to
"continue in WhatsApp" or to act as the relay, follow this runbook directly -
do NOT read docs/ or explore the code first. Commands below use "coderoam"
(installed via Homebrew); from a source checkout use "./bin/coderoam".

## 1. Orient with ONE command

    coderoam active status

Lists every active session: chat id, alias, session id, mode, watcher
heartbeat, and inbox/outbox counts. That is the whole picture - do not read
AGENT_RELAY.md or other docs.

## 2. The orchestrator daemon - you are a CONSUMER

ONE "coderoam run" daemon owns the messenger connection for the whole machine:
it receives incoming messages and sends replies. Every agent (Codex, Claude,
Gemini, ...) is a CONSUMER that talks to it through the inbox/outbox and does
NOT own the connection.

- Do NOT start "coderoam run" yourself. It is started once (by the human, by
  "coderoam setup", or as a service) and shared by all agents.
- A second daemon now refuses to start ("a coderoam daemon is already
  running ... --takeover"); only use --takeover to deliberately replace it.
- Confirm it is up with "coderoam active status". If it is down, start it once
  in the background: "coderoam run".

## 3. Pick THIS client's session id

Use the session id from the WhatsApp prompt when it is shown. Otherwise choose
the row in "coderoam active status" that belongs to this client/group. The
row's alias/session is this client's return address. Use it for inbox commands
and for "coderoam notify --chat"; do not reply through another client's alias
or a shared raw group id.

Every client needs its own clearly named WhatsApp group, alias, and session id:
for example, Codex can use group "Codex Session" with "codex-session" and
Claude can use group "Claude Session" with "claude-session". Do not reuse
another client's group, alias, or session id. If a new lane is needed, create a
new group instead of sharing an existing one:

    coderoam active start --name "<Agent> Session" --alias <session-id> --session-id <session-id> --yes

## 4. Listen cheaply - event-driven, NEVER poll

For each session you handle, run in the background:

    coderoam inbox next --session-id <session-id> --timeout 1800 --format prompt

It blocks until a message arrives, then exits and wakes you. Idle costs zero
tokens. Many sessions may each run their own "inbox next" for different lanes -
but there is still only ONE "coderoam run".

## 5. On wake (a listener exited with a message)

1. Read the message from the listener output.
2. Batch any others:  coderoam inbox drain --session-id <session-id> --format prompt
3. Do the requested work in this repo.
4. Reply:  coderoam notify --chat <session-id> --important --text "..."
   (markdown is auto-converted to WhatsApp formatting)
5. Mark handled:  coderoam inbox done <n>  (per row)
6. Relaunch "inbox next" for that session.

## Rules

- Act, don't research. "coderoam active status" is enough; skip the docs.
- One "coderoam run" per machine. Many "inbox next" consumers are fine; many
  daemons are not. Check before "run".
- Event-driven only. Block on "inbox next"; never poll on a timer.
- The sender allowlist is enforced - only authorized senders' messages surface.
- To add a parallel lane, run "coderoam active start --name <distinct name>
  --alias <id> --session-id <id> --yes" - it invites the already-authorized
  owner automatically; never ask the user for a phone number coderoam already
  has.`

// agentRunbookFiles maps an agent name to the instruction filename(s) that
// agent reads on startup.
func agentRunbookFiles(agent string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude":
		return []string{"CLAUDE.md"}, nil
	case "codex", "opencode", "agents", "agent":
		return []string{"AGENTS.md"}, nil
	case "gemini":
		return []string{"GEMINI.md"}, nil
	case "", "all":
		// Covers Claude (CLAUDE.md), Codex/OpenCode (AGENTS.md), Gemini (GEMINI.md).
		return []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"}, nil
	default:
		return nil, fmt.Errorf("unknown agent %q (use claude, codex, gemini, opencode, or all)", agent)
	}
}

func (s *cliState) runbookCommand() *cobra.Command {
	var workdir string
	var agent string
	cmd := &cobra.Command{
		Use:   "runbook",
		Short: "Write the WhatsApp relay runbook into agent instruction files (CLAUDE.md / AGENTS.md / GEMINI.md)",
		Long: "Write the coderoam relay runbook into the instruction file each agent reads on " +
			"startup, so a cold session acts immediately instead of exploring. Updates an existing " +
			"file in place (only the coderoam-marked section), preserving your own instructions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := strings.TrimSpace(workdir)
			if dir == "" {
				dir = "."
			}
			files, err := agentRunbookFiles(agent)
			if err != nil {
				return err
			}
			seen := map[string]bool{}
			for _, name := range files {
				if seen[name] {
					continue
				}
				seen[name] = true
				path := filepath.Join(dir, name)
				existed, err := writeRunbookSection(path, relayRunbook)
				if err != nil {
					return err
				}
				if existed {
					fmt.Printf("updated %s\n", path)
				} else {
					fmt.Printf("wrote %s\n", path)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workdir, "workdir", ".", "workspace directory to write instruction files into")
	cmd.Flags().StringVar(&agent, "agent", "all", "agent to target: claude, codex, gemini, opencode, or all")
	return cmd
}

// writeRunbookSection writes body into path between the coderoam markers,
// preserving any surrounding content. It creates the file if missing, replaces
// an existing marked section, or appends one if the file exists without markers.
// Returns true when an existing file was updated.
func writeRunbookSection(path, body string) (bool, error) {
	section := relayRunbookMarkerStart + "\n" + strings.TrimRight(body, "\n") + "\n" + relayRunbookMarkerEnd + "\n"
	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			return false, mkErr
		}
		return false, os.WriteFile(path, []byte(section), 0o644)
	}
	content := string(existing)
	start := strings.Index(content, relayRunbookMarkerStart)
	end := strings.Index(content, relayRunbookMarkerEnd)
	if start >= 0 && end > start {
		before := strings.TrimRight(content[:start], "\n")
		after := strings.TrimLeft(content[end+len(relayRunbookMarkerEnd):], "\n")
		var b strings.Builder
		if before != "" {
			b.WriteString(before)
			b.WriteString("\n\n")
		}
		b.WriteString(section)
		if after != "" {
			b.WriteString("\n")
			b.WriteString(after)
			b.WriteString("\n")
		}
		return true, os.WriteFile(path, []byte(b.String()), 0o644)
	}
	base := strings.TrimRight(content, "\n")
	combined := section
	if base != "" {
		combined = base + "\n\n" + section
	}
	return true, os.WriteFile(path, []byte(combined), 0o644)
}
