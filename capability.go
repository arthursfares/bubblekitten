package bubblekitten

import "os"

// Support represents what we know about the connected terminal's ability
// to render the Kitty graphics protocol via Unicode placeholders.
type Support int

const (
	// SupportUnknown means detection hasn't run or was inconclusive; the
	// component will optimistically try to render (most terminals treat
	// unsupported APC sequences and PUA glyphs harmlessly).
	SupportUnknown Support = iota
	// SupportYes means the terminal is believed to support the protocol.
	SupportYes
	// SupportNo means the terminal is known not to support it; the
	// component falls back to a plain placeholder box instead of emitting
	// any escape codes.
	SupportNo
)

// DetectSupport makes a best-effort, non-blocking guess at whether the
// current terminal supports the Kitty graphics protocol with Unicode
// placeholders (added in kitty 0.28), based on environment variables set
// by known-supporting terminals.
//
// This is a heuristic, not a protocol-level query: querying the terminal
// properly requires reading its raw APC response, which the default
// Bubble Tea input reader does not surface to Update. If your program
// already has a raw-input hook, you can send the query built by
// buildSupportQuery yourself and call WithSupport once you have an answer.
func DetectSupport() Support {
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return SupportYes
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "ghostty", "WezTerm", "vscode":
		// Ghostty implements Unicode placeholders. WezTerm and VS Code's
		// terminal have partial/evolving kitty graphics support; treat as
		// unknown-but-likely rather than a hard yes.
		if os.Getenv("TERM_PROGRAM") == "ghostty" { return SupportYes }
		return SupportUnknown
	}
	if os.Getenv("TERM") == "xterm-kitty" { return SupportYes }
	return SupportUnknown
}
